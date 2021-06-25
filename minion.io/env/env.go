package env

import (
	"os"

	"go.uber.org/zap"
	"minion.io/shared"
)

func EnvHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	MyInfo.Node = os.Getenv("MY_NODE_NAME")
	MyInfo.Pod = os.Getenv("MY_POD_NAME")
	MyInfo.Namespace = os.Getenv("MY_POD_NAMESPACE")
	MyInfo.PodIp = os.Getenv("MY_POD_IP")
	MyInfo.Id = os.Getenv("MY_POD_CLUSTER")
	MyInfo.MongoUri = os.Getenv("MY_MONGO_URI")
	MyInfo.Host = os.Getenv("HOSTNAME")

	sugar.Infow("env", "Node", MyInfo.Node)
	sugar.Infow("env", "Pod", MyInfo.Pod)
	sugar.Infow("env", "Namespace", MyInfo.Namespace)
	sugar.Infow("env", "PodIp", MyInfo.PodIp)
	sugar.Infow("env", "Id", MyInfo.Id)
	sugar.Infow("env", "MongoUri", MyInfo.MongoUri)
}
