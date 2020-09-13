/*
 * stats.go: Packet statistics, e.g., drop
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 *
 * To run test locally, do following:
 *     1. nc -l 127.0.0.1 10000
 *     2. go test -v
 */

package stats

import (
	"go.uber.org/zap"
	"testing"
)

func TestPakDrop(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	pak := []byte("GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 23\r\n\r\n<body>\r\nhello\r\n</body>")
	for i := 0; i < 2; i++ {
		PakDrop(pak, "access denied", sugar)
	}
}
