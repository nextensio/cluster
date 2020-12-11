/*
 * env.go: parse environment variables
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package env

import (
	"go.uber.org/zap"
	"minion.io/common"
	"os"
	"strconv"
)

func EnvHandler(sugar *zap.SugaredLogger) {
	common.MyInfo.Node = os.Getenv("MY_NODE_NAME")
	if common.MyInfo.Node == "" {
		common.MyInfo.Node = "k8s-worker1"
	}
	common.MyInfo.Pod = os.Getenv("MY_POD_NAME")
	if common.MyInfo.Pod == "" {
		common.MyInfo.Pod = "tom.com"
	}
	common.MyInfo.Namespace = os.Getenv("MY_POD_NAMESPACE")
	if common.MyInfo.Namespace == "" {
		common.MyInfo.Namespace = "default"
	}
	common.MyInfo.PodIp = os.Getenv("MY_POD_IP")
	if common.MyInfo.PodIp == "" {
		common.MyInfo.PodIp = "127.0.0.1"
	}
	common.MyInfo.Id = os.Getenv("MY_POD_CLUSTER")
	if common.MyInfo.Id == "" {
		common.MyInfo.Id = "sjc"
	}
	common.MyInfo.DnsIp = os.Getenv("MY_DNS")
	if common.MyInfo.DnsIp == "" {
		common.MyInfo.DnsIp = "157.230.160.64"
	}
	common.MyInfo.MongoUri = os.Getenv("MY_MONGO_URI")
	if common.MyInfo.MongoUri == "" {
		common.MyInfo.MongoUri = "127.0.0.1"
	}
	common.MyInfo.Sim, _ = strconv.ParseBool(os.Getenv("MY_SIM_TEST"))
	if common.MyInfo.Sim == false {
		common.MyInfo.Sim = false
	}
	common.MyInfo.C_suffix = "-" + common.MyInfo.Namespace
	common.MyInfo.C_server = common.MyInfo.Node + ".node.consul"
	common.MyInfo.C_port = 8500
	common.MyInfo.C_scheme = "http"
	sugar.Infow("env", "Node", common.MyInfo.Node)
	sugar.Infow("env", "Pod", common.MyInfo.Pod)
	sugar.Infow("env", "Namespace", common.MyInfo.Namespace)
	sugar.Infow("env", "PodIp", common.MyInfo.PodIp)
	sugar.Infow("env", "Id", common.MyInfo.Id)
	sugar.Infow("env", "DnsIp", common.MyInfo.DnsIp)
	sugar.Infow("env", "MongoUri", common.MyInfo.MongoUri)
	sugar.Infow("env", "Sim", common.MyInfo.Sim)
	sugar.Infow("-", "C_suffix", common.MyInfo.C_suffix)
	sugar.Infow("-", "C_server", common.MyInfo.C_server)
	sugar.Infow("-", "C_port", common.MyInfo.C_port)
	sugar.Infow("-", "C_scheme", common.MyInfo.C_scheme)
}
