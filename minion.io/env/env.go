/*
 * env.go: parse environment variables
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package env

import (
	"os"
	"strconv"

	"go.uber.org/zap"
	"minion.io/shared"
)

func EnvHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	MyInfo.Node = os.Getenv("MY_NODE_NAME")
	if MyInfo.Node == "" {
		MyInfo.Node = "k8s-worker1"
	}
	MyInfo.Pod = os.Getenv("MY_POD_NAME")
	if MyInfo.Pod == "" {
		MyInfo.Pod = "tom.com"
	}
	MyInfo.Namespace = os.Getenv("MY_POD_NAMESPACE")
	if MyInfo.Namespace == "" {
		MyInfo.Namespace = "default"
	}
	MyInfo.PodIp = os.Getenv("MY_POD_IP")
	if MyInfo.PodIp == "" {
		MyInfo.PodIp = "127.0.0.1"
	}
	MyInfo.Id = os.Getenv("MY_POD_CLUSTER")
	if MyInfo.Id == "" {
		MyInfo.Id = "sjc"
	}
	MyInfo.DnsIp = os.Getenv("MY_DNS")
	if MyInfo.DnsIp == "" {
		MyInfo.DnsIp = "157.230.160.64"
	}
	MyInfo.MongoUri = os.Getenv("MY_MONGO_URI")
	if MyInfo.MongoUri == "" {
		MyInfo.MongoUri = "127.0.0.1"
	}
	MyInfo.Sim, _ = strconv.ParseBool(os.Getenv("MY_SIM_TEST"))
	if MyInfo.Sim == false {
		MyInfo.Sim = false
	}
	MyInfo.C_suffix = "-" + MyInfo.Namespace
	MyInfo.C_server = MyInfo.Node + ".node.consul"
	MyInfo.C_port = 8500
	MyInfo.C_scheme = "http"
	sugar.Infow("env", "Node", MyInfo.Node)
	sugar.Infow("env", "Pod", MyInfo.Pod)
	sugar.Infow("env", "Namespace", MyInfo.Namespace)
	sugar.Infow("env", "PodIp", MyInfo.PodIp)
	sugar.Infow("env", "Id", MyInfo.Id)
	sugar.Infow("env", "DnsIp", MyInfo.DnsIp)
	sugar.Infow("env", "MongoUri", MyInfo.MongoUri)
	sugar.Infow("env", "Sim", MyInfo.Sim)
	sugar.Infow("-", "C_suffix", MyInfo.C_suffix)
	sugar.Infow("-", "C_server", MyInfo.C_server)
	sugar.Infow("-", "C_port", MyInfo.C_port)
	sugar.Infow("-", "C_scheme", MyInfo.C_scheme)
}
