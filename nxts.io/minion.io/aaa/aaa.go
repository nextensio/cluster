/*
 * aaa.go - aaa package for interfaceing with opa library
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package aaa

import (
	"go.uber.org/zap"
)

var mocklib bool = true
var initDone bool = false
var myns string
var myuri string

func AaaInit(ns string, uri string, pod string, s *zap.SugaredLogger) int {
	s.Debugf("aaa: mongo uri %v\n", uri)
	s.Debugf("aaa: initialised in namespace %v of type %v\n", ns, pod)
	if mocklib {
		return 0
	} else {
		// put opa call
	}
	return 0
}

func UsrAllowed(id string, s *zap.SugaredLogger) bool {
	s.Debugf("aaa: user allowed %v\n", id)
	if mocklib {
		return true
	} else {
		// put opa call
	}
	return true
}

func UsrJoin(pod string, id string, s *zap.SugaredLogger) {
	s.Debugf("aaa: user joined %v of type %v\n", id, pod)
	if initDone == false {
		AaaInit(myns, myuri, pod, s)
		initDone = true
	}
	if mocklib {
		return
	} else {
		if pod == "agent" {
			// put opa call
		}
	}
}

func UsrLeave(pod string, id string, s *zap.SugaredLogger) {
	s.Debugf("aaa: user left %v of type %v\n", id, pod)
	if mocklib {
		return
	} else {
		if pod == "agent" {
			// put opa call
		}
	}
}

func GetUsrAttr(pod string, id string, s *zap.SugaredLogger) (string, bool) {
	s.Debugf("aaa: usr attr %v of type %v\n", id, pod)
	if mocklib {
		if pod == "controller" {
			return "", false
		}
		usrAttr := "{ dept: computer-science, team: blue }"
		return usrAttr, true
	} else {
		// put opa call
	}
	return "", false
}

func AccessOk(pod string, id string, attr string, s *zap.SugaredLogger) bool {
	s.Debugf("aaa: access %v of type %v with attr %v\n", id, pod, attr)
	if pod == "agent" {
		return true
	}
	if mocklib {
		return true
	} else {
		// put opa call
	}

	return true
}

func AaaStart(ns string, uri string, s *zap.SugaredLogger) {
	myns = ns
	myuri = uri

	if mocklib {
		return
	}
	// put opa call
}
