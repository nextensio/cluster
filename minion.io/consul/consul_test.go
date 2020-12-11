/*
 * consul_test.go: Testing
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

/*
 * pre-requisite
 *  ADD dns entry
 *  vi /etc/hosts and add 157.230.175.239 k8s-worker1.node.consul
 * Following is optional if register and deregister tests are skipped
 *  cd GOSRC/files/test
 *  ./shorty.py --domain --name gateway.sjc.nextensio.net --to_name connector-10 --service agent-10 --port 443 --ssl --services "agent-10"
 */
package consul

import (
	"go.uber.org/zap"
	"minion.io/common"
	"testing"
)

const (
	serviceName   = "agent-10"
	serviceNameNS = "agent-10-default"
)

var logger *zap.Logger
var sugar *zap.SugaredLogger

func TestInit(t *testing.T) {
	common.MyInfo.Id = "sjc"
	common.MyInfo.Node = "k8s-worker1"
	common.MyInfo.Namespace = "default"
	common.MyInfo.DnsIp = "157.230.160.64"
	common.MyInfo.Pod = "tom.com"
	common.MyInfo.PodIp = "1.1.1.1"
	common.MyInfo.Register = true
	logger, _ = zap.NewProduction()
	//logger, _ = zap.NewDevelopment()
	sugar = logger.Sugar()
}

func TestConsulRegister(t *testing.T) {
	defer logger.Sync()

	testData := [common.MaxService]string{serviceName}

	err := RegisterConsul(testData, sugar)
	if err != nil {
		t.Error("Failure to register")
	}
}

func TestConsulDnsLookupOne(t *testing.T) {
	defer logger.Sync()

	fwd, _ := ConsulDnsLookup(serviceNameNS, sugar)

	if fwd.DestType != common.LocalDest {
		t.Error("expected local but not local")
	}

	if fwd.Dest != "tom-com-in.default.svc.cluster.local" {
		t.Errorf("expected tom-com-in.default.svc.cluster.local but got %s", fwd.Dest)
	}
}

func TestConsulDnsLookupTwo(t *testing.T) {
	defer logger.Sync()

	common.MyInfo.Id = "ric"
	fwd, _ := ConsulDnsLookup(serviceNameNS, sugar)

	if fwd.DestType != common.RemoteDest {
		t.Error("expected remote but local")
	}

	if fwd.Dest != "gateway.sjc.nextensio.net" {
		t.Errorf("expected gateway.sjc.nextensio.net but got %s", fwd.Dest)
	}
}

func TestConsulDeRegister(t *testing.T) {
	defer logger.Sync()

	testData := [common.MaxService]string{serviceName}

	err := DeRegisterConsul(testData, sugar)
	if err != nil {
		t.Error("Failure to Deregister")
	}
}
