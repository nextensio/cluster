package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	common "gitlab.com/nextensio/common/go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"minion.io/consul"
	"minion.io/policy"
	"minion.io/router"
	"minion.io/shared"
)

var MyInfo shared.Params

var lumlog = &lumberjack.Logger{
	Filename:   "app_debug.log",
	MaxSize:    5,
	MaxBackups: 3,
	MaxAge:     3,
}

func lumberjackZapHook(e zapcore.Entry) error {
	lumlog.Write([]byte(fmt.Sprintf("%+v\n", e)))
	return nil
}

func ArgHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	inPortPtr := flag.Int("iport", 80, "inside port")
	outPortPtr := flag.Int("oport", 443, "outside port")
	healthPortPtr := flag.Int("hport", 8080, "consul healthcheck port")

	flag.Parse()

	sugar.Infof("iport: %d", *inPortPtr)
	sugar.Infof("oport: %d", *outPortPtr)
	sugar.Infof("oport: %d", *healthPortPtr)

	MyInfo.Iport = *inPortPtr
	MyInfo.Oport = *outPortPtr
	MyInfo.HealthPort = *healthPortPtr
}

func EnvHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	MyInfo.Node = os.Getenv("MY_NODE_NAME")
	MyInfo.Pod = os.Getenv("MY_POD_NAME")
	MyInfo.PodType = os.Getenv("MY_POD_TYPE")
	MyInfo.Namespace = os.Getenv("MY_POD_NAMESPACE")
	MyInfo.PodIp = os.Getenv("MY_POD_IP")
	MyInfo.Id = os.Getenv("MY_POD_CLUSTER")
	MyInfo.MongoUri = os.Getenv("MY_MONGO_URI")
	MyInfo.Host = os.Getenv("HOSTNAME")
	MyInfo.JaegerCollector = os.Getenv("JAEGER_COLLECTOR")

	sugar.Infow("env", "Node", MyInfo.Node)
	sugar.Infow("env", "Pod", MyInfo.Pod)
	sugar.Infow("env", "PodType", MyInfo.PodType)
	sugar.Infow("env", "Namespace", MyInfo.Namespace)
	sugar.Infow("env", "PodIp", MyInfo.PodIp)
	sugar.Infow("env", "Id", MyInfo.Id)
	sugar.Infow("env", "MongoUri", MyInfo.MongoUri)
}

func main() {
	common.MAXBUF = (64 * 1024)
	ctx := context.Background()
	//logger, _ := zap.NewProduction(zap.Hooks(lumberjackZapHook))
	logger, _ := zap.NewDevelopment(zap.Hooks(lumberjackZapHook))
	defer logger.Sync()
	sugar := logger.Sugar()
	ArgHandler(sugar, &MyInfo)
	EnvHandler(sugar, &MyInfo)
	// Create a Jaegertracer instance for gap spans (spans for the period the pkt is on the wire). Jaeger UI needs
	// different Jaegertrace instance service name to display the spans in different color. So, we create this
	// instance just for gap spans to be displayed in different colors.
	closer := router.InitJaegerTrace(MyInfo.Namespace+"-onwire-trace", &MyInfo, sugar, true)
	if closer != nil {
		defer closer.Close()
	}
	router.RouterInit(sugar, &MyInfo, ctx)
	go policy.NxtOpaInit(MyInfo.Namespace, MyInfo.Pod, MyInfo.Id+consul.RemotePostPrefix, MyInfo.MongoUri, sugar)

	// Do kill -USR1 <pid of minion> to get all stack traces in app_debug.log
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGUSR1)
	go func() {
		for {
			<-sigc
			// ... do something ...
			buf := make([]byte, 1<<16)
			runtime.Stack(buf, true)
			sugar.Debugf("%s", buf)
			router.DumpInfo(sugar)
		}
	}()

	for {
		time.Sleep(86400 * time.Second)
	}
}
