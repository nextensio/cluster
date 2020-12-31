/*
 * consul.go: regsiter services with consul
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package consul

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
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
	consulRetries    = 5
	LocalSuffix      = ".svc.cluster.local"
	RemotePrePrefix  = "gateway."
	RemotePostPrefix = ".nextensio.net"
)

// Json structure for registering consul service
type Meta struct {
	Cluster string
	Pod     string
}

type Entry struct {
	ID      string
	Name    string
	Address string
	Meta    Meta
}

// Json structure for consul key-value result
type Consul struct {
	Kvs []Kv
}

type Kv struct {
	LockIndex   int
	Key         string
	Flags       int
	Value       string
	CreateIndex int
	ModifyIndex int
}

/*
 * Register DNS entry and key value pair for the service
 */
func RegisterConsul(MyInfo *shared.Params, service []string, sugar *zap.SugaredLogger) (e error) {
	if MyInfo.Sim == true {
		// In sim mode
		return nil
	}

	if MyInfo.Register == false {
		// Don't register service with Consul as per cmd line arg
		return nil
	}

	var dns Entry
	dns.Address = MyInfo.PodIp
	dns.Meta.Cluster = MyInfo.Id

	var url string
	var data string
	var h string
	var js []byte
	var r *http.Request
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + MyInfo.Namespace
		sugar.Debugf("Consul: registering service %s at %s", h, url)
		data = MyInfo.Id
		r, _ = http.NewRequest("PUT", url+"/cluster", strings.NewReader(data))
		i := 1
		_, e := myClient.Do(r)
		if e != nil {
			for ; i < consulRetries; i = i + 1 {
				time.Sleep(1 * 1000 * time.Millisecond)
				_, e = myClient.Do(r)
				if e == nil {
					break
				}
			}
		}
		if e != nil {
			sugar.Errorf("Consul: http PUT %s at %s failed with %v retries", data, url+"/cluster", consulRetries)
		}
		data = strings.Replace(MyInfo.Pod, ".", "-", -1)
		r, _ = http.NewRequest("PUT", url+"/pod", strings.NewReader(data))
		i = 0
		for ; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(1 * 1000 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http PUT %s at %s failed with %v retries", data, url+"/pod", consulRetries)
		}
		dns.Meta.Pod = data
		dns.ID = h + "-" + MyInfo.Namespace
		dns.Name = h + "-" + MyInfo.Namespace
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/register"
		js, _ = json.Marshal(dns)
		r, _ = http.NewRequest("PUT", url, bytes.NewReader(js))
		r.Header.Add("Content-Type", "application/json")
		r.Header.Add("Accept-Charset", "UTF-8")
		i = 0
		for ; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(1 * 1000 * time.Millisecond)
		}
		if i < consulRetries && e == nil {
			sugar.Debugf("Consul: registered via http PUT at %s", url)
			sugar.Debugf("Consul: registered service json %s", js)
		} else {
			sugar.Errorf("Consul: failed to register via http PUT at %s", url)
			sugar.Errorf("Consul: failed to register service json %s", js)
		}
	}

	return nil
}

/*
 * DeRegister DNS entry and key value pair for the service
 */
func DeRegisterConsul(MyInfo *shared.Params, service []string, sugar *zap.SugaredLogger) (e error) {
	if MyInfo.Sim == true {
		return nil
	}

	if MyInfo.Register == false {
		return nil
	}

	var url string
	var h string
	var r *http.Request
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + MyInfo.Namespace
		sugar.Debugf("Consul: Deregistering service %s at %s", h, url)
		r, _ = http.NewRequest("DELETE", url+"/cluster", nil)
		i := 1
		_, e := myClient.Do(r)
		if e != nil {
			for ; i < consulRetries; i = i + 1 {
				time.Sleep(1 * 1000 * time.Millisecond)
				_, e = myClient.Do(r)
				if e == nil {
					break
				}
			}
		}
		if e != nil {
			sugar.Errorf("Consul: http DELETE of %s at %s failed with %v retries", h, url+"/cluster", consulRetries)
		}
		r, _ = http.NewRequest("DELETE", url+"/pod", nil)
		i = 0
		for ; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(1 * 1000 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http DELETE of %s at %s failed with %v retries", h, url+"/pod", consulRetries)
		}
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/deregister/" + h + "-" + MyInfo.Namespace
		r, _ = http.NewRequest("PUT", url, nil)
		i = 0
		for ; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(1 * 1000 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http PUT of nil at %s failed with %v retries", url, consulRetries)
		}
	}

	return nil
}

//# curl -v -s http://k8s-worker1.node.consul:8500/v1/kv/tom-com-default?recurse
//*   Trying 10.50.0.38...
//* TCP_NODELAY set
//* Connected to k8s-worker1.node.consul (10.50.0.38) port 8500 (#0)
//> GET /v1/kv/tom-com-default?recurse HTTP/1.1
//> Host: k8s-worker1.node.consul:8500
//> User-Agent: curl/7.61.1
//> Accept: */*
//>
//< HTTP/1.1 200 OK
//< Content-Type: application/json
//< Vary: Accept-Encoding
//< X-Consul-Index: 367484
//< X-Consul-Knownleader: true
//< X-Consul-Lastcontact: 0
//< Date: Tue, 11 Jun 2019 01:51:21 GMT
//< Content-Length: 235
//<
//* Connection #0 to host k8s-worker1.node.consul left intact
//[{"LockIndex":0,"Key":"tom-com-default/cluster","Flags":0,"Value":"c2pj","CreateIndex":367483,"ModifyIndex":367483},{"LockIndex":0,"Key":"tom-com-default/pod","Flags":0,"Value":"dG9tLWNvbQ==","CreateIndex":367484,"ModifyIndex":367484}]/ #

// Reply we get is top level array instead of a full JSON object. So we need to handle it
// differently when unmarshalling it
func ConsulHttpLookup(MyInfo *shared.Params, name string, sugar *zap.SugaredLogger) (fwd shared.Fwd, e error) {
	sugar.Infof("consul kv lookup for %s", name)

	h := strings.Replace(name, ".", "-", -1)
	url := "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "?recurse"
	sugar.Debugf("Consul: HTTP lookup at %s", url)
	i := 1
	resp, e := myClient.Get(url)
	if e != nil {
		for ; i < consulRetries; i = i + 1 {
			time.Sleep(1 * 1000 * time.Millisecond)
			resp, e = myClient.Get(url)
			if e == nil {
				break
			}
		}
	}
	if e != nil {
		sugar.Errorf("Consul: http GET for %s at %s failed with %v retries", h, url, consulRetries)
		return fwd, e
	}

	defer resp.Body.Close()
	r, e := ioutil.ReadAll(resp.Body)
	if e != nil {
		sugar.Errorf("Consul: http response read err %v", e)
		return fwd, e
	}
	// create a slice for storing JSON array
	kvs := make([]Kv, 0)
	e = json.Unmarshal(r, &kvs)
	if e != nil {
		sugar.Errorf("Consul: json response unmarshal err %v", e)
		return fwd, e
	}
	tmp, _ := base64.StdEncoding.DecodeString(kvs[0].Value)
	fwd.Id = string(tmp)
	tmp, _ = base64.StdEncoding.DecodeString(kvs[1].Value)
	fwd.Pod = string(tmp)
	if fwd.Id == MyInfo.Id {
		// Same cluster
		if fwd.Pod == MyInfo.Pod {
			// Same pod
			fwd.DestType = shared.SelfDest
			fwd.Dest = name
			sugar.Debugf("Consul: destination %s of type SelfDest", name)
		} else {
			// Different pod
			fwd.DestType = shared.LocalDest
			fwd.Dest = fwd.Pod + "-in." + MyInfo.Namespace + LocalSuffix
			sugar.Debugf("Consul: destination %s of type LocalDest", fwd.Dest)
		}
	} else {
		// Different cluster
		fwd.DestType = shared.RemoteDest
		fwd.Dest = RemotePrePrefix + fwd.Id + RemotePostPrefix
		sugar.Debugf("Consul: destination %s of type RemoteDest", fwd.Dest)
	}

	return fwd, nil
}

//#  dig @10.110.209.4 tom-com-default.query.consul SRV
//
//; <<>> DiG 9.11.3-1ubuntu1.7-Ubuntu <<>> @10.110.209.4 tom-com-default.query.consul SRV
//; (1 server found)
//;; global options: +cmd
//;; Got answer:
//;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 32913
//;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 3
//;; WARNING: recursion requested but not available
//
//;; OPT PSEUDOSECTION:
//; EDNS: version: 0, flags:; udp: 4096
//;; QUESTION SECTION:
//;tom-com-default.query.consul.  IN      SRV
//
//;; ANSWER SECTION:
//tom-com-default.query.consul. 0 IN      SRV     1 1 0 0a32800e.addr.sjc.consul.
//
//;; ADDITIONAL SECTION:
//0a32800e.addr.sjc.consul. 0     IN      A       10.50.128.14
//k8s-worker2.node.sjc.consul. 0  IN      TXT     "consul-network-segment="
//
//;; Query time: 1 msec
//;; SERVER: 10.110.209.4#53(10.110.209.4)
//;; WHEN: Tue Jun 11 07:05:47 UTC 2019
//;; MSG SIZE  rcvd: 170

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

	for _, v := range resp.Answer {
		if srv, ok := v.(*dns.SRV); ok {
			target := srv.Target
			s := strings.Split(target, ".")
			if s[2] == MyInfo.Id {
				hfwd, e := ConsulHttpLookup(MyInfo, name, sugar)
				if e != nil {
					return fwd, e
				}
				return hfwd, nil
			} else {
				fwd.DestType = shared.RemoteDest
				fwd.Dest = RemotePrePrefix + s[2] + RemotePostPrefix
				fwd.Pod = ""
				sugar.Debugf("Consul: destination %s of type RemoteDest", fwd.Dest)
				return fwd, nil
			}
		}
	}

	return fwd, errors.New("not found")
}
