/*
 * consul.go: regsiter services with consul
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package consul

import (
    "encoding/json"
    "encoding/base64"
    "net/http"
    "io/ioutil"
    "strings"
    "bytes"
    "errors"
    "time"
    "go.uber.org/zap"
    "github.com/miekg/dns"
    "minion.io/common"
)

/*
 * Create HTTP client with 10 second timeout
 */
var myClient = &http.Client{Timeout: 10 * time.Second}

/*
 * Register DNS entry and key value pair for the service
 */
func RegisterConsul (service [common.MaxService]string, sugar *zap.SugaredLogger) (e error) {
    if common.MyInfo.Sim == true {
        return nil
    }

    if common.MyInfo.Register == false {
        return nil
    }

    sugar.Infof("Registering...")
    entryJson := `{"ID":"candy-com-default", "Name":"candy-com-default", "Address":"10.50.128.5", "Meta":{"cluster":"sjc", "pod":"candy.com.default"}}`
    var dns common.Entry
    json.Unmarshal([]byte(entryJson), &dns)

    dns.Address = common.MyInfo.PodIp
    dns.Meta.Cluster = common.MyInfo.Id

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
        url = "http://" + common.MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + common.MyInfo.Namespace
        sugar.Debugf("%s", url)
        data = common.MyInfo.Id
        r, _ = http.NewRequest("PUT", url + "/cluster", strings.NewReader(data))
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
        }
        data = strings.Replace(common.MyInfo.Pod, ".", "-", -1)
        r, _ = http.NewRequest("PUT", url + "/pod", strings.NewReader(data))
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
        }
        dns.Meta.Pod = data
        dns.ID = h + "-" + common.MyInfo.Namespace
        dns.Name = h + "-" + common.MyInfo.Namespace
        sugar.Debugw("json", "Meta.Pod:", dns.Meta.Pod)
        sugar.Debugw("json", "ID:", dns.ID)
        sugar.Debugw("json", "Name:", dns.Name)
        sugar.Debugw("json", "Address:", dns.Address)
        sugar.Debugw("json", "Meta.Cluster:", dns.Meta.Cluster)
        url = "http://" + common.MyInfo.Node + ".node.consul:8500/v1/agent/service/register"
        sugar.Debugf("%s", url)
        js, _ = json.Marshal(dns)
        r, _ = http.NewRequest("PUT", url, bytes.NewReader(js))
        r.Header.Add("Content-Type", "application/json")
        r.Header.Add("Accpet-Charset", "UTF-8")
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
        }
    }

    return nil
}

/*
 * DeRegister DNS entry and key value pair for the service
 */
func DeRegisterConsul (service [common.MaxService]string, sugar *zap.SugaredLogger) (e error) {
    if common.MyInfo.Sim == true {
        return nil
    }

    if common.MyInfo.Register == false {
        return nil
    }

    sugar.Infof("DeRegistering...")

    var url string
    var h string
    var r *http.Request
    for _, val := range service {
        if val == "" {
            break
        }
        h = strings.Replace(val, ".", "-", -1)
        url = "http://" + common.MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "-" + common.MyInfo.Namespace
        sugar.Debugf("%s", url)
        r, _ = http.NewRequest("DELETE", url + "/cluster", nil)
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
        }
        r, _ = http.NewRequest("DELETE", url + "/pod", nil)
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
        }
        url = "http://" + common.MyInfo.Node + ".node.consul:8500/v1/agent/service/deregister/" + h + "-" + common.MyInfo.Namespace
        sugar.Debugf("%s", url)
        r, _ = http.NewRequest("PUT", url, nil)
        _, e = myClient.Do(r)
        if e != nil {
            sugar.Errorw("http", "err", e)
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
func ConsulHttpLookup(name string, sugar *zap.SugaredLogger) (fwd common.Fwd, e error) {
    sugar.Infow("consul kv lookup", "name:", name)

    h := strings.Replace(name, ".", "-", -1)
    url := "http://" + common.MyInfo.Node + ".node.consul:8500/v1/kv/" + h + "?recurse"
    sugar.Debugf("%s", url)
    resp, e := myClient.Get(url)
    if e != nil {
	sugar.Errorw("http", "err", e)
        return fwd, e
    }

    defer resp.Body.Close()
    r, e  := ioutil.ReadAll(resp.Body)
    if e != nil {
	sugar.Errorw("http", "err", e)
        return fwd, e
    }
    // create a slice for storing JSON array
    kvs := make([]common.Kv, 0)
    e = json.Unmarshal(r, &kvs)
    if e != nil {
	sugar.Errorw("json", "err", e)
        return fwd, e
    }
    tmp, _ := base64.StdEncoding.DecodeString(kvs[0].Value)
    fwd.Id = string(tmp)
    tmp, _ = base64.StdEncoding.DecodeString(kvs[1].Value)
    fwd.Pod = string(tmp)
    if fwd.Id == common.MyInfo.Id {
        if fwd.Pod == common.MyInfo.Pod {
            fwd.DestType = common.SelfDest
            fwd.Dest = name
        } else {
            fwd.DestType = common.LocalDest
            fwd.Dest = fwd.Pod + "-in." + common.MyInfo.Namespace + common.LocalSuffix
        }
    } else {
        fwd.DestType = common.RemoteDest
        fwd.Dest = common.RemotePrePrefix + fwd.Id + common.RemotePostPrefix
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

func ConsulDnsLookup(name string, sugar *zap.SugaredLogger) (fwd common.Fwd, e error) {
    sugar.Infow("consul dns lookup", "name:", name)
    
    qType, _ := dns.StringToType["SRV"]
    fqdn_name := name + ".query.consul."
    client := new(dns.Client)
    msg := &dns.Msg{}
    msg.SetQuestion(fqdn_name, qType)
    resp, _, e := client.Exchange(msg, common.MyInfo.DnsIp + ":53")
    if e != nil {
        sugar.Errorw("dns", "err", e)
        return fwd, e
    }
    sugar.Debugw("dns", "resp:", resp)

    for _, v := range resp.Answer {
        if srv, ok := v.(*dns.SRV); ok {
            target := srv.Target
            s := strings.Split(target, ".")
            if s[2] == common.MyInfo.Id {
                hfwd, e := ConsulHttpLookup(name, sugar)
                if e != nil {
                    return fwd, e
                }
                return hfwd, nil
            } else {
                fwd.DestType = common.RemoteDest
                fwd.Dest = common.RemotePrePrefix + s[2] + common.RemotePostPrefix
                fwd.Pod = ""
                return fwd, nil
            }
        }
    }

    return fwd, errors.New("not found")
}
