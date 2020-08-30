/*
 * parse.go: Simple HTTP/1.1 parser, not complete -- only handles Http Header
 *          having content-length
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package http

import (
    "bytes"
    "strings"
    "strconv"
    "regexp"
    "go.uber.org/zap"
)

var state struct {
    header []byte
    body []byte
    buf []byte
    clen int
    plen int
    cursor int
    header_complete bool
    body_complete bool
    partial_body bool
}

func stateInit() {
    state.header = state.header[:0]
    state.body = state.body[:0]
    state.buf = state.buf[:0]
    state.clen = 0
    state.plen = 0
    state.cursor = 0
    state.header_complete = false
    state.body_complete = false
    state.partial_body = false
}

func GetHeaders() (s string) {
    return string(state.header)
}

func GetBody() (s string) {
    return string(state.body)
}

func GetParseLen() (l int) {
    return state.plen
}

func IsHeaderComplete() (a bool) {
    return state.header_complete
}

func IsBodyComplete() (a bool) {
    return state.body_complete
}

func parseHeader(sugar *zap.SugaredLogger) (b bool) {
    idx := bytes.Index(state.buf, []byte("\r\n\r\n"))
    if idx < 0 {
        return false
    }
    state.header = append(state.header, state.buf[:idx+4]...)
    state.buf = state.buf[idx+4:]
    var length = regexp.MustCompile(`content-length:\s?(.*)\r\n`)
    match := length.Find(state.header)
    if match != nil {
        s := bytes.SplitN(match, []byte(":"), 2)
        state.clen, _ = strconv.Atoi(strings.TrimSpace(string(s[1])))
    } else {
        state.clen = 0
    }
    state.header_complete = true
    state.plen = idx + 4 + state.clen
    return true
}

func parseBody(sugar *zap.SugaredLogger) (b bool) {
    if state.clen <= len(state.buf) {
        state.body = append(state.body, state.buf[:state.clen]...)
        state.body_complete = true
        state.partial_body = false
        return true
    } else {
        state.partial_body = true
        return false
    }
}

/*
 * Parses only 1 complete HTTP/1.1 packet and returns. Caller calls it again
 * to parse another HTTP/1.1 packet either in same buffer or new buffer
 */
func Execute(data []byte, length int, sugar *zap.SugaredLogger) (l int) {
    if state.body_complete == true {
        stateInit()
    }
    if len(data) <= 0 {
        return length
    }
    for {
        switch {
            case state.header_complete != true:
                state.buf = append(state.buf, data...)
                t1 := parseHeader(sugar)
                if t1 == false {
                    state.cursor += length
                    return length
                }
            case state.body_complete != true:
                t2 := parseBody(sugar)
                if t2 == false {
                    state.cursor += length
                    return length
                }
            default:
                sugar.Debugw("parse", "header:", string(state.header))
                sugar.Debugw("parse", "body:", string(state.body))
                return state.plen - state.cursor
        }
    }
}
