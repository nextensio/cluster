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
	inPortPtr := flag.Int("iport", 80, "inside port")
	outPortPtr := flag.Int("oport", 443, "outside port")

	flag.Parse()

	sugar.Infof("iport: %d", *inPortPtr)
	sugar.Infof("oport: %d", *outPortPtr)

	MyInfo.Iport = *inPortPtr
	MyInfo.Oport = *outPortPtr
}
