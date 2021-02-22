/*
 * minion.go:
 *
 * Workers of Nextensio world. They work tirelessly connecting two islands
 * in a secure way. Let the work begin.
 * Worker listens on two ports:
 * * 80 : for worker-to-worker communication
 * * 443 : for agents/connectors to connect to the worker
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package main

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"minion.io/aaa"
	"minion.io/args"
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
	ctx := context.Background()
	//logger, _ := zap.NewProduction(zap.Hooks(lumberjackZapHook))
	logger, _ := zap.NewDevelopment(zap.Hooks(lumberjackZapHook))
	defer logger.Sync()
	sugar := logger.Sugar()
	args.ArgHandler(sugar, &MyInfo)
	env.EnvHandler(sugar, &MyInfo)
	router.RouterInit(sugar, &MyInfo, ctx)
	go aaa.AaaStart(MyInfo.Namespace, MyInfo.MongoUri, sugar)
	for {
		time.Sleep(86400 * time.Second)
	}
}
