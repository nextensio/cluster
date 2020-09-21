/*
 * util.go: common routines
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package router

import (
	"bufio"
	"net"
)

func UtilWrite(conn net.Conn, pak []byte) (int, error) {
	writer := bufio.NewWriter(conn)
	n, e := writer.Write(pak)
	if e == nil {
		e = writer.Flush()
	}
	return n, e
}
