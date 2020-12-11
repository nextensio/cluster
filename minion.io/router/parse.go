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
	imoved          bool
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
	// Move out headers into separate buffer. buf now contains just the content (full or partial)
	state.header = append(state.header, state.buf[:idx+4]...)
	state.buf = state.buf[idx+4:]
	// search for content-length header
	var length = regexp.MustCompile(`content-length:\s?(.*)\r\n`)
	match := length.Find(state.header)
	if match != nil {
		// Found. Extract content length value into clen.
		s := bytes.SplitN(match, []byte(":"), 2)
		state.clen, _ = strconv.Atoi(strings.TrimSpace(string(s[1])))
	} else {
		state.clen = 0
	}
	state.header_complete = true
	state.plen = idx + 4 + state.clen
	// plen now points to end of http content (packet length)
	return true
}

func parseBody(state *State, sugar *zap.SugaredLogger) bool {
	if state.clen <= len(state.buf) {
		// We already have all the content in buf
		state.body = append(state.body, state.buf[:state.clen]...)
		state.body_complete = true
		state.partial_body = false
		return true
	} else {
		// We don't have all the content in buf yet.
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
	state.imoved = false
	for {
		switch {
		case state.header_complete != true:
			// Add incoming data to buf and note total length of data received
			state.buf = append(state.buf, data...)
			state.cursor += length
			state.imoved = true
			t1 := parseHeader(state, sugar)
			if t1 == false {
				// End of headers not found even after using up all received data.
				return length
			}
		case state.body_complete != true:
			if state.imoved == false {
				// Incoming data has not been pulled in yet, so do it now
				state.buf = append(state.buf, data...)
				state.cursor += length
				state.imoved = true
			}
			t2 := parseBody(state, sugar)
			if t2 == false {
				// End of content not found yet even after using up all received data
				return length
			}
		default:
			// Full http message received - header + body
			// return what part of length has been used up from data[]
			u := length - (state.cursor - state.plen)
			if state.plen != (state.clen + len(state.header)) {
				// HTTP message length != header length + content length !
				sugar.Errorf("parse: hlen=%v, clen=%v, plen=%v, used=%v",
					len(state.header), state.clen, state.plen, u)
			}
			return u
		}
	}
}
