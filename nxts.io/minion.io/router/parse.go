/*
 * parse.go: Simple HTTP/1.1 parser, not complete -- only handles Http Header
 *          having content-length
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package router

import (
	"bytes"
	"go.uber.org/zap"
	"regexp"
	"strconv"
	"strings"
)

type State struct {
	header          []byte
	body            []byte
	buf             []byte
	clen            int
	plen            int
	cursor          int
	header_complete bool
	body_complete   bool
	partial_body    bool
}

func stateInit(state *State) {
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

func GetHeaders(state *State) []byte {
	return state.header
}

func GetBody(state *State) []byte {
	return state.body
}

func GetParseLen(state *State) int {
	return state.plen
}

func IsHeaderComplete(state *State) bool {
	return state.header_complete
}

func IsBodyComplete(state *State) bool {
	return state.body_complete
}

func InitCtx() *State {
	var ctx = &State{}
	stateInit(ctx)
	return ctx
}

func parseHeader(state *State, sugar *zap.SugaredLogger) bool {
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

func parseBody(state *State, sugar *zap.SugaredLogger) bool {
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
func Execute(state *State, data []byte, length int, sugar *zap.SugaredLogger) int {
	if state.body_complete == true {
		stateInit(state)
	}
	if len(data) <= 0 {
		return length
	}
	for {
		switch {
		case state.header_complete != true:
			state.buf = append(state.buf, data...)
			t1 := parseHeader(state, sugar)
			if t1 == false {
				state.cursor += length
				return length
			}
		case state.body_complete != true:
			t2 := parseBody(state, sugar)
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
