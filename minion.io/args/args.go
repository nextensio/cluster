package args

import (
	"flag"

	"go.uber.org/zap"
	"minion.io/shared"
)

func ArgHandler(sugar *zap.SugaredLogger, MyInfo *shared.Params) {
	inPortPtr := flag.Int("iport", 80, "inside port")
	outPortPtr := flag.Int("oport", 443, "outside port")
	healthPortPtr := flag.Int("hport", 8080, "outside port")

	flag.Parse()

	sugar.Infof("iport: %d", *inPortPtr)
	sugar.Infof("oport: %d", *outPortPtr)
	sugar.Infof("oport: %d", *healthPortPtr)

	MyInfo.Iport = *inPortPtr
	MyInfo.Oport = *outPortPtr
	MyInfo.HealthPort = *healthPortPtr
}
