/*
 * consul_test.go: Testing
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

/*
 * pre-requisite
 * ../../files/test/shorty.py --domain --name gateway.sjc.nextensio.net --to_name candy.com --service tom.com --port 443 --ssl --services "tom.com"
 */
package consul

import (
    "minion.io/common"
    "go.uber.org/zap"
    "testing"
)

func TestConsulDnsLookupOne(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()

    common.MyInfo.Id = "sjc"
    common.MyInfo.Node = "k8s-worker1"
    common.MyInfo.Namespace = "default"
    common.MyInfo.DnsIp = "157.230.160.64"
    fwd, _ := ConsulDnsLookup("tom-com-default", sugar)
   
    if fwd.Local != true {
        t.Error("expected local but not local")
    }

    if fwd.Dest != "tom-com-in.default.svc.cluster.local" {
        t.Errorf("expected tom-com-in.default.svc.cluster.local but got %s", fwd.Dest)
    }
}

func TestConsulDnsLookupTwo(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()

    common.MyInfo.Id = "ric"
    common.MyInfo.Node = "k8s-worker1"
    common.MyInfo.Namespace = "default"
    common.MyInfo.DnsIp = "157.230.160.64"
    fwd, _ := ConsulDnsLookup("tom-com-default", sugar)
   
    if fwd.Local == true {
        t.Error("expected remote but local")
    }

    if fwd.Dest != "gateway.sjc.nextensio.net" {
        t.Errorf("expected gateway.sjc.nextensio.net but got %s", fwd.Dest)
    }
}
