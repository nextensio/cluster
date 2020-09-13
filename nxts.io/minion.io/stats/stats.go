/*
 * stats.go: Packet statistics, e.g., drop
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package stats

import (
	"bytes"
	"go.uber.org/zap"
	"net"
	"regexp"
	"strconv"
	"strings"
)

var nullConn net.Conn = nil

func PakDrop(pak []byte, reason string, s *zap.SugaredLogger) {
	s.Debugf("stats: packet drop due to %v\n", reason)
	if nullConn == nil {
		portStr := strconv.Itoa(10000)
		servAddr := strings.Join([]string{"127.0.0.1", portStr}, ":")
		conn, e := net.Dial("tcp", servAddr)
		if e != nil {
			s.Debugw("stats:", "error", e)
			return
		}
		nullConn = conn
	} else {
		s.Debug("stats: connection exists\n")
	}
	z := bytes.SplitN(pak, []byte("\r\n\r\n"), 2)
	drop := "x-nextensio-drop: " + reason + "\r\n"
	dropb := []byte(drop)
	pak = bytes.Join([][]byte{z[0], dropb}, []byte("\r\n"))
	r := regexp.MustCompile("content-length:.+")
	pak = r.ReplaceAll(pak, []byte("content-length: 0"))
	_, e := nullConn.Write(pak)
	if e != nil {
		nullConn.Close()
		nullConn = nil
	}
}
