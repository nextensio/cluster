/*
 * parse_test.go: Testing
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package router

import (
    "go.uber.org/zap"
    "testing"
)

func TestExecutedHalf(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()
    s1 := "GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty\r\nx-next"
    s2 := "ensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 5\r\n\r\nhello"
    var data []byte
    data = []byte(s1)
    var l1, l2 int
    state := InitCtx()
    l1 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) == true {
        t.Error("parsing failure")
    }
    data = []byte(s2)
    l2 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    if l1 + l2 != len(s1) + len(s2) {
        t.Error("parsed length not correct")
    }
}

func TestExecuteOne(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()
    s := "GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 5\r\n\r\nhello"
    var data []byte
    data = []byte(s)
    var l int
    state := InitCtx()
    l = Execute(state, data, len(s), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    if l != len(s) {
        t.Error("parsed length not correct")
    }
}

func TestExecuteTwo(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()
    s := "GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 5\r\n\r\nhelloGET / HTTP/1.1\r\nHost: gateway.shc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 6\r\n\r\nhello2"
    var data []byte
    data = []byte(s)
    var l1, l2 int
    state := InitCtx()
    l1 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    data = data[l1:]
    l2 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    if l1+l2 != len(s) {
        t.Error("parsed length not correct")
    }
}

func TestExecuteTwoAndHalf(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()
    s := "GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 5\r\n\r\nhelloGET / HTTP/1.1\r\nHost: gateway.shc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 6\r\n\r\nhello2GET / HTTP/1.1\r\nHost: place\r\nuser-agent: shorty\r\nx-nextensio-for:"
    var data []byte
    data = []byte(s)
    var l1, l2, l3 int
    state := InitCtx()
    l1 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    data = data[l1:]
    l2 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    if l1+l2 >= len(s) {
        t.Error("parsed length not correct")
    }
    data = data[l2:]
    l3 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) == true {
        t.Error("parsing failure")
    }
    if l3 >= len(s) {
        t.Error("parsed length not correct")
    }
}

func TestExecuteHalfAndTwo(t *testing.T) {
    logger, _ := zap.NewProduction()
    defer logger.Sync()
    sugar := logger.Sugar()
    s1 := "GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\n"
    s2 := "content-length: 5\r\n\r\nhelloGET / HTTP/1.1\r\nHost: gateway.shc.nextensio.net\r\nuser-agent: ./shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 6\r\n\r\nhello2GET / HTTP/1.1\r\nHost: place\r\nuser-agent: shorty\r\nx-nextensio-for:  127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 7\r\n\r\nhello21"
    var data []byte
    data = []byte(s1)
    var l1, l2, l3, l4 int
    state := InitCtx()
    l1 = Execute(state, data, len(data), sugar)
    if l1 != len(s1) {
        t.Error("parsed length not correct")
    }
    if IsBodyComplete(state) == true {
        t.Error("parsing failure")
    }
    data = []byte(s2)
    l2 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    data = data[l2:]
    l3 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    data = data[l3:]
    l4 = Execute(state, data, len(data), sugar)
    if IsBodyComplete(state) != true {
        t.Error("parsing failure")
    }
    if l1+l2+l3+l4 != len(s1) + len(s2) {
        t.Error("parsed length not correct")
    }
}
