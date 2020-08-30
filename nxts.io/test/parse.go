/*
 * http.go: Simple HTTP/1.1 parser, not complete -- only handles Http Header
 *          having content-length
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package main

import (
    "bytes"
    "strings"
    "strconv"
    "fmt"
)

func main() {
    var clen int
    match := []byte("content-length: 5\r\n")
    if match != nil {
        s := bytes.SplitN(match, []byte(":"), 2)
        clen, _ = strconv.Atoi(strings.TrimSpace(string(s[1])))
    } else {
        clen = 0
    }
    fmt.Println(clen)
}
