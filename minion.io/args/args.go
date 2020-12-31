/*
 * args.go: handle the various arguments for this program
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package args

import (
	"flag"

	"go.uber.org/zap"
	"minion.io/shared"
)

func ArgHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	tunnelPtr := flag.Bool("tunnel", false, "run in tunnel mode")
	consulDnsPtr := flag.Bool("consul_dns", false, "use consul dns")
	consulHttpPtr := flag.Bool("consul_http", false, "use consul http")
	registerPtr := flag.Bool("register", false, "register service to consul")
	inPortPtr := flag.Int("iport", 8001, "inside port")
	outPortPtr := flag.Int("oport", 8002, "outside port")
	ipPtr := flag.String("ip", "127.0.0.1", "ip address to listen")

	flag.Parse()

	sugar.Infof("tunnel: %t", *tunnelPtr)
	sugar.Infof("consul_dns: %t", *consulDnsPtr)
	sugar.Infof("consul_http: %t", *consulHttpPtr)
	sugar.Infof("register: %t", *registerPtr)
	sugar.Infof("iport: %d", *inPortPtr)
	sugar.Infof("oport: %d", *outPortPtr)
	sugar.Infof("ip: %s", *ipPtr)

	MyInfo.Tunnel = *tunnelPtr
	MyInfo.UseDns = *consulDnsPtr
	MyInfo.UseHttp = *consulHttpPtr
	MyInfo.Register = *registerPtr
	MyInfo.Iport = *inPortPtr
	MyInfo.Oport = *outPortPtr
	MyInfo.ListenIp = *ipPtr
}
