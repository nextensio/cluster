package main

import (
	"context"
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
	"minion.io/aaa"
	"minion.io/args"
	"minion.io/consul"
	"minion.io/env"
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

func main() {
	common.MAXBUF = (64 * 1024)
	ctx := context.Background()
	//logger, _ := zap.NewProduction(zap.Hooks(lumberjackZapHook))
	logger, _ := zap.NewDevelopment(zap.Hooks(lumberjackZapHook))
	defer logger.Sync()
	sugar := logger.Sugar()
	args.ArgHandler(sugar, &MyInfo)
	env.EnvHandler(sugar, &MyInfo)
	router.RouterInit(sugar, &MyInfo, ctx)
	go aaa.AaaStart(MyInfo.Namespace, MyInfo.Pod, MyInfo.Id+consul.RemotePostPrefix, MyInfo.MongoUri, sugar, router.DisconnectUser)

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
		}
	}()

	for {
		time.Sleep(86400 * time.Second)
	}
}
