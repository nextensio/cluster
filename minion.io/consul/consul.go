package consul

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
	"minion.io/shared"
)

var conn *dns.Conn
var myDnsClient *dns.Client

/*
 * Create HTTP client with 10 second timeout
 */
var myClient = &http.Client{Timeout: 10 * time.Second}

const (
	LocalSuffix      = ".svc.cluster.local"
	RemotePostPrefix = ".nextensio.net"
)

// Json structure for registering consul service
type Meta struct {
	NextensioCluster string
	NextensioPod     string
}

type Entry struct {
	ID      string
	Name    string
	Address string
	Meta    Meta
}

type Check struct {
	ID                             string
	Name                           string
	ServiceID                      string
	HTTP                           string
	Interval                       string
	Timeout                        string
	DeregisterCriticalServiceAfter string
	FailuresBeforeCritical         int
	SuccessBeforePassing           int
}

func makeCheck(MyInfo *shared.Params, serviceID string) Check {
	return Check{
		ID:                             serviceID,
		Name:                           serviceID,
		ServiceID:                      serviceID,
		HTTP:                           "http://" + MyInfo.Host + "." + MyInfo.Pod + "." + MyInfo.Namespace + ".svc.cluster.local:" + fmt.Sprint(MyInfo.HealthPort),
		Interval:                       "5s",
		Timeout:                        "5s",
		DeregisterCriticalServiceAfter: "60s",
		FailuresBeforeCritical:         5,
		SuccessBeforePassing:           3,
	}
}

/*
 * Establish connection to the DNS at the startup for consul lookups.
 */
func DialDnsConn(MyInfo *shared.Params, sugar *zap.SugaredLogger) *dns.Conn {
	var err error
	if conn == nil {
		myDnsClient = new(dns.Client)
		dnsIp := MyInfo.Id + "-consul-dns.consul-system.svc.cluster.local"
		conn, err = myDnsClient.Dial(dnsIp + ":53")
		if err != nil {
			sugar.Debugf("Consul: DNS Dial error - %s", err.Error())
		}
	}
	return conn
}

/*
 * Register DNS entry for the service
 */
func RegisterConsul(MyInfo *shared.Params, service []string, sugar *zap.SugaredLogger) (e error) {
	var dns Entry
	dns.Address = MyInfo.PodIp
	dns.Meta.NextensioCluster = MyInfo.Id

	for _, val := range service {
		h := strings.Replace(val, ".", "-", -1)
		dns.Meta.NextensioPod = MyInfo.Pod
		dns.Name = h + "-" + MyInfo.Namespace
		// ID needs to be unique per cpod replica .. every replica is registering the
		// same service but with different ID. This ensures that even if one replica
		// unregisters the service, the consul catalog will still have the service from
		// other replicas
		dns.ID = dns.Name + "-" + MyInfo.Host
		url := "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/register"
		js, e := json.Marshal(dns)
		if e != nil {
			sugar.Errorf("Consul: failed to make make json at %s, error %s", url, e)
			return e
		}
		r, e := http.NewRequest("PUT", url, bytes.NewReader(js))
		if e != nil {
			sugar.Errorf("Consul: failed to make http request at %s, error %s", url, e)
			return e
		}
		r.Header.Add("Content-Type", "application/json")
		r.Header.Add("Accept-Charset", "UTF-8")
		resp, e := myClient.Do(r)
		if e == nil && resp.StatusCode == 200 {
			sugar.Debugf("Consul: registered via http PUT at %s", url)
			sugar.Debugf("Consul: registered service json %s", js)
		} else {
			status := -1
			if resp != nil {
				status = resp.StatusCode
			}
			sugar.Errorf("Consul: failed to register via http PUT at %s, error %s, %d", url, e, status)
			sugar.Errorf("Consul: failed to register service json %s", js)
			return e
		}

		check := makeCheck(MyInfo, dns.ID)
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/check/register"
		js, e = json.Marshal(check)
		if e != nil {
			sugar.Errorf("Consul: failed to make make json at %s, error %s", url, e)
			return e
		}
		r, e = http.NewRequest("PUT", url, bytes.NewReader(js))
		if e != nil {
			sugar.Errorf("Consul: failed to make http request at %s, error %s", url, e)
			return e
		}
		r.Header.Add("Content-Type", "application/json")
		r.Header.Add("Accept-Charset", "UTF-8")
		resp, e = myClient.Do(r)
		if e == nil && resp.StatusCode == 200 {
			sugar.Debugf("Consul: registered check via http PUT at %s", url)
		} else {
			status := -1
			if resp != nil {
				status = resp.StatusCode
			}
			sugar.Errorf("Consul: failed to register via http PUT at %s, error %s, %d", url, e, status)
			return e
		}
	}

	return nil
}

/*
 * DeRegister DNS entry and PodIP:Podname key:value pair for the service
 * Service being deregistered automatically deletes the consul health check
 */
func DeRegisterConsul(MyInfo *shared.Params, service []string, sugar *zap.SugaredLogger) (e error) {
	var err error
	for _, val := range service {
		h := strings.Replace(val, ".", "-", -1)
		sid := h + "-" + MyInfo.Namespace + "-" + MyInfo.Host
		url := "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/deregister/" + sid
		r, e := http.NewRequest("PUT", url, nil)
		if e != nil {
			sugar.Errorf("Consul: deregister failed to make http request at %s, error %s", url, e)
			return e
		}
		resp, e := myClient.Do(r)
		if e != nil || resp.StatusCode != 200 {
			status := -1
			if resp != nil {
				status = resp.StatusCode
			}
			sugar.Errorf("Consul: http PUT of nil at %s failed err %s, code %s %d", url, e, status)
			// Well, keep going and delete all the services even if this one failed.
			// If the service is really going away from the pod, the health check will
			// eventually fail and remove this service in approx 1.5 minutes
		}
	}

	return err
}

func ConsulDnsLookup(MyInfo *shared.Params, name string, sugar *zap.SugaredLogger) (fwd shared.Fwd, e error) {
	sugar.Infof("consul dns lookup for %s", name)

	dnsIp := MyInfo.Id + "-consul-dns.consul-system.svc.cluster.local"
	qType, _ := dns.StringToType["SRV"]
	fqdn_name := name + ".query.consul."
	msg := &dns.Msg{}
	msg.SetQuestion(fqdn_name, qType)
	// This additional OPT record is required to get the TXT records
	// in the answer if there are multiple answers. Even without this,
	// with just one record in the answer, we get TXT records properly,
	// but with more than one record, without the OPT, we dont get TXT
	o := new(dns.OPT)
	o.Hdr.Name = "."
	o.Hdr.Rrtype = dns.TypeOPT
	o.SetDo()
	o.SetUDPSize(4096)
	msg.Extra = append(msg.Extra, o)

	if conn == nil {
		conn = DialDnsConn(MyInfo, sugar)
	}
	resp, t, e := myDnsClient.ExchangeWithConn(msg, conn)
	if e != nil {
		sugar.Errorf("Consul: dns lookup for %s at %s failed with %s error", name, dnsIp, e)
		return fwd, e
	}

	sugar.Debugf("Consul: DNS lookup for %s returned %v answers, with %s latency", name, len(resp.Answer), t)
	if len(resp.Answer) < 1 {
		return fwd, errors.New("not found")
	}
	podName := ""
	for _, t := range resp.Extra {
		if t, ok := t.(*dns.TXT); ok {
			reg, _ := regexp.Compile("NextensioPod:(.+)")
			for _, s := range t.Txt {
				match := reg.FindStringSubmatch(s)
				if len(match) == 2 {
					podName = match[1]
				}
			}
		}
	}
	if podName == "" {
		return fwd, errors.New("Pod not found")
	}

	// Just always pick the first answer. It will be load balanced (round robin) in case
	// of multiple answers.
	if srv, ok := resp.Answer[0].(*dns.SRV); ok {
		target := srv.Target
		s := strings.Split(target, ".")
		fwd.Id = s[2]
		fwd.Pod = podName
		if s[2] != MyInfo.Id {
			fwd.DestType = shared.RemoteDest
			fwd.Dest = s[2] + RemotePostPrefix
		} else {
			if fwd.Pod == MyInfo.Pod {
				fwd.DestType = shared.SelfDest
				fwd.Dest = name
			} else {
				fwd.DestType = shared.LocalDest
				fwd.Dest = fwd.Pod + "-in." + MyInfo.Namespace + LocalSuffix
			}
		}
		sugar.Debugf("Consul: destination %s / %s of type %d", fwd.Dest, fwd.Pod, fwd.DestType)
		return fwd, nil
	}

	return fwd, errors.New("not found")
}
