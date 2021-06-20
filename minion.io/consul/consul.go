package consul

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"
	"minion.io/shared"
)

/*
 * Create HTTP client with 10 second timeout
 */
var myClient = &http.Client{Timeout: 10 * time.Second}

const (
	consulRetries    = 2
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

/*
 * Register DNS entry for the service
 */
func RegisterConsul(MyInfo *shared.Params, service []string, uuid string, sugar *zap.SugaredLogger) (e error) {
	var dns Entry
	dns.Address = MyInfo.PodIp
	dns.Meta.NextensioCluster = MyInfo.Id

	var url string
	var h string
	var js []byte
	var r *http.Request
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		dns.Meta.NextensioPod = MyInfo.Pod
		dns.Name = h + "-" + MyInfo.Namespace
		// ID needs to be unique per Consul agent
		dns.ID = dns.Name + "-" + MyInfo.Pod
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/register"
		js, _ = json.Marshal(dns)
		r, _ = http.NewRequest("PUT", url, bytes.NewReader(js))
		r.Header.Add("Content-Type", "application/json")
		r.Header.Add("Accept-Charset", "UTF-8")
		for i := 0; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if i := 0; i < consulRetries && e == nil {
			sugar.Debugf("Consul: registered via http PUT at %s", url)
			sugar.Debugf("Consul: registered service json %s", js)
		} else {
			sugar.Errorf("Consul: failed to register via http PUT at %s", url)
			sugar.Errorf("Consul: failed to register service json %s", js)
			DeRegisterConsul(MyInfo, service, uuid, sugar)
			return e
		}
	}

	return nil
}

/*
 * DeRegister DNS entry and PodIP:Podname key:value pair for the service
 */
func DeRegisterConsul(MyInfo *shared.Params, service []string, uuid string, sugar *zap.SugaredLogger) (e error) {
	var url string
	var h string
	var sid string
	var r *http.Request
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		sid = h + "-" + MyInfo.Namespace + "-" + MyInfo.Pod
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/deregister/" + sid
		r, _ = http.NewRequest("PUT", url, nil)
		for i := 0; i < 2*consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http PUT of nil at %s failed with %v retries", url, 2*consulRetries)
		}
	}

	return nil
}

func ConsulDnsLookup(MyInfo *shared.Params, name string, sugar *zap.SugaredLogger) (fwd shared.Fwd, e error) {
	sugar.Infof("consul dns lookup for %s", name)

	qType, _ := dns.StringToType["SRV"]
	fqdn_name := name + ".query.consul."
	client := new(dns.Client)
	msg := &dns.Msg{}
	msg.SetQuestion(fqdn_name, qType)
	i := 1
	resp, _, e := client.Exchange(msg, MyInfo.DnsIp+":53")
	if e != nil {
		for ; i < consulRetries; i = i + 1 {
			time.Sleep(1 * 1000 * time.Millisecond)
			resp, _, e = client.Exchange(msg, MyInfo.DnsIp+":53")
			if e == nil {
				break
			}
		}
	}
	if e != nil {
		sugar.Errorf("Consul: dns lookup for %s at %s failed with %v retries", name, MyInfo.DnsIp, consulRetries)
		return fwd, e
	}

	sugar.Debugf("Consul: DNS lookup for %s returned %v answers", name, len(resp.Answer))
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
		sugar.Debugf("Consul: destination %s of type %s", fwd.Dest, fwd.DestType)
		return fwd, nil
	}

	return fwd, errors.New("not found")
}
