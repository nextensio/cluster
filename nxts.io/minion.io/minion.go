/*
 * main.go:
 *
 * Workers of Nextensio world. They work tirelessly connecting two islands
 * in a secure way. Let the work begin.
 * Worker listens on two ports:
 * * 8001 : for worker-to-worker communication
 * * 8002 : for islands to connect to the worker
 * communication happens over websocket. Format of data exchanged over websocket
 * is json or http/1.1 or http/2 formatted.
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package main

import (
    "go.uber.org/zap"
    "minion.io/common"
    "minion.io/args"
    "minion.io/env"
    "minion.io/consul"
)

var services [common.MaxService]string

func main() {
    //logger, _ := zap.NewProduction()
    logger, _ := zap.NewDevelopment()
    defer logger.Sync()
    sugar := logger.Sugar()
    args.ArgHandler(sugar)
    env.EnvHandler(sugar)
    consul.RegisterConsul(services, sugar)
    consul.DeRegisterConsul(services, sugar)
}
