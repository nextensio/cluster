package consul

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
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
	RemotePrePrefix  = ""
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
 * Register DNS entry and PodIP:Podname key:value pair for the service
 */
func RegisterConsul(MyInfo *shared.Params, service []string, uuid string, sugar *zap.SugaredLogger) (e error) {
	var dns Entry
	dns.Address = MyInfo.PodIp
	dns.Meta.Cluster = MyInfo.Id

	var url string
	var h string
	var js []byte
	var r *http.Request
	npodip := strings.Replace(MyInfo.PodIp, ".", "-", -1)
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + MyInfo.Namespace + "-" + npodip
		sugar.Debugf("Consul: registering pod %s at IP %s for service %s at %s", MyInfo.Pod, MyInfo.PodIp, h, url)
		r, _ = http.NewRequest("PUT", url, strings.NewReader(MyInfo.Pod))
		i := 0
		for ; i < consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http PUT %s for IP %s at %s failed with %v retries", MyInfo.Pod, MyInfo.PodIp, url, consulRetries)
			DeRegisterConsul(MyInfo, service, uuid, sugar)
			return e
		}
		dns.Meta.Pod = MyInfo.Pod
		dns.Name = h + "-" + MyInfo.Namespace
		// ID needs to be unique per Consul agent
		dns.ID = dns.Name + "-" + MyInfo.Pod + "-" + uuid
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
			time.Sleep(10 * time.Millisecond)
		}
		if i < consulRetries && e == nil {
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
	npodip := strings.Replace(MyInfo.PodIp, ".", "-", -1)
	for _, val := range service {
		if val == "" {
			break
		}
		h = strings.Replace(val, ".", "-", -1)
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + MyInfo.Namespace + "-" + npodip
		sugar.Debugf("Consul: Deregistering pod %s at IP %s for service %s at %s", MyInfo.Pod, MyInfo.PodIp, h, url)
		r, _ = http.NewRequest("DELETE", url, nil)
		i := 0
		for ; i < 2*consulRetries; i = i + 1 {
			_, e = myClient.Do(r)
			if e == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if e != nil {
			sugar.Errorf("Consul: http DELETE of %s for IP %s at %s failed with %v retries", MyInfo.Pod, MyInfo.PodIp, url, 2*consulRetries)
		}
		sid = h + "-" + MyInfo.Namespace + "-" + MyInfo.Pod + "-" + uuid
		url = "http://" + MyInfo.Node + ".node.consul:8500/v1/agent/service/deregister/" + sid
		r, _ = http.NewRequest("PUT", url, nil)
		i = 0
		for ; i < 2*consulRetries; i = i + 1 {
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

//# curl -v -s http://k8s-worker1.node.consul:8500/v1/kv/tom-com-default-10-244-0-16
//
//[
// {"LockIndex":0,
//  "Key":"tom-com-default-10-244-0-16",
//  "Flags":0,
//  "Value":"pod4",
//  "CreateIndex":367483,
//  "ModifyIndex":367483}
// }
//]
// Lookup KV store using pod IP as key and get pod name from "Value" of json reply
func ConsulKvLookup(MyInfo *shared.Params, name string, podip string, sugar *zap.SugaredLogger) (fwd shared.Fwd, e error) {
	sugar.Infof("consul kv lookup for %s at IP %s", name, podip)

	// remove dots from dotted decimal IP address
	npodip := strings.Replace(podip, ".", "-", -1)
	// name contains namespace
	url := "http://" + MyInfo.Node + ".node.consul:8500/v1/kv/" + name + "-" + npodip
	sugar.Debugf("Consul: KV lookup at %s", url)
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
		sugar.Errorf("Consul: KV lookup of pod name for %s at %s failed with %v retries", name, url, consulRetries)
		return fwd, e
	}

	defer resp.Body.Close()
	r, e := ioutil.ReadAll(resp.Body)
	if e != nil {
		sugar.Errorf("Consul: KV lookup http response read err %v", e)
		return fwd, e
	}
	// for storing JSON array from kv store
	var kvs []Kv
	e = json.Unmarshal(r, &kvs)
	if e != nil {
		sugar.Errorf("Consul: json response unmarshal err %v", e)
		return fwd, e
	}
	tmp, _ := base64.StdEncoding.DecodeString(kvs[0].Value)
	fwd.Pod = string(tmp)
	fwd.PodIp = podip
	fwd.Id = MyInfo.Id
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

// DNS lookup answer section returns destination pod IP and cluster.
// If destination pod is in same cluster, use pod IP to look up pod name in kv store.
// Kv lookup can be avoided if pod IP is enough to forward frame. For now, we keep
// both pod name and IP to minimize changes.
// If destination pod is in a different cluster, get destination cluster to forward
// the frame to.
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
	// Just always pick the first answer. It will be load balanced (round robin) in case
	// of multiple answers.
	if srv, ok := resp.Answer[0].(*dns.SRV); ok {
		target := srv.Target
		s := strings.Split(target, ".")
		// Consul DNS lookup returns pod IP in compressed hex format. Convert it to dotted decimal.
		podip := ipString(s[0], sugar)
		if s[2] == MyInfo.Id {
			// Target pod is in same cluster. Do KV lookup to get name from IP address
			hfwd, e := ConsulKvLookup(MyInfo, name, podip, sugar)
			if e != nil {
				return fwd, e
			}
			return hfwd, nil
		} else {
			// target pod is in a remote cluster
			fwd.Id = s[2]
			fwd.DestType = shared.RemoteDest
			fwd.Dest = RemotePrePrefix + s[2] + RemotePostPrefix
			fwd.Pod = ""
			sugar.Debugf("Consul: destination %s of type RemoteDest", fwd.Dest)
			return fwd, nil
		}
	}

	return fwd, errors.New("not found")
}

// Return the dotted decimal string form of the IP address from a compressed hex string.
func ipString(ip string, sugar *zap.SugaredLogger) string {

	var ips string
	var decn [4]int64

	// Assume IPv4 for now, convert to dotted decimal notation.
	p4 := ip

	i := 0
	for j := 0; j < 3; j++ {
		decn[j], _ = strconv.ParseInt(p4[i:i+2], 16, 16)
		i = i + 2
	}
	decn[3], _ = strconv.ParseInt(p4[6:8], 16, 16)
	ips = fmt.Sprintf("%v.%v.%v.%v", decn[0], decn[1], decn[2], decn[3])
	sugar.Debugf("Consul: ipString got %s, returned %s", ip, ips)
	return ips
}
