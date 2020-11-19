/*
 * aaa.go - aaa package for interfaceing with opa library
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package aaa

import (
	"go.uber.org/zap"
	"minion.io/authz"
)

var mocklib bool = false
var myns string
var myuri string

func AaaInit(ns string, uri string, s *zap.SugaredLogger) int {
	s.Debugf("aaa: mongo uri %v\n", uri)
	s.Debugf("aaa: initialised in namespace %v\n", ns)
	if mocklib {
		return 0
	}
	return (authz.NxtAaaInit(ns, uri, s))
}

func UsrAllowed(id string, s *zap.SugaredLogger) bool {
	var val bool

	if mocklib {
		val = true
	} else {
		val = authz.NxtUsrAllowed(id)
	}

	s.Debugf("aaa: user allowed %v : result %v\n", id, val)

	return val
}

func UsrJoin(pod string, id string, s *zap.SugaredLogger) {
	s.Debugf("aaa: user joined %v of type %v\n", id, pod)
	if mocklib {
		return
	}
	if pod == "agent" {
		authz.NxtUsrJoin(id)
	}
}

func UsrLeave(pod string, id string, s *zap.SugaredLogger) {
	s.Debugf("aaa: user left %v of type %v\n", id, pod)
	if mocklib {
		return
	}
	if pod == "agent" {
		authz.NxtUsrLeave(id)
	}
}

func GetUsrAttr(pod string, id string, s *zap.SugaredLogger) (string, bool) {
	var usrAttr string
	var val bool

	if mocklib {
		if pod == "connector" {
			return "", false
		}
		usrAttr = "{ dept: computer-science, team: blue }"
	} else {
		if pod == "connector" {
			return "", false
		}
		usrAttr, val = authz.NxtGetUsrAttr(id)
	}
	s.Debugf("aaa: usr %s of type %s : attr %s, val %v\n",
		id, pod, usrAttr, val)
	return usrAttr, val
}

func AccessOk(pod string, id string, attr string, s *zap.SugaredLogger) bool {
	var val bool

	if pod == "agent" {
		return true
	}
	if mocklib {
		val = true
	} else {
		val = authz.NxtAccessOk(id, attr)
	}

	s.Debugf("aaa: access %v of type %v with attr %v : resullt %v\n",
		id, pod, attr, val)

	return val
}

func AaaStart(ns string, uri string, s *zap.SugaredLogger) {
	myns = ns
	myuri = uri

	AaaInit(myns, myuri, s)
}

func RouteLookup(pod string, uid string, host string, s *zap.SugaredLogger) string {
	if mocklib {
		s.Debugf("aaa: Route lookup for user %s to host %s with mocklib\n", uid, host)
		return ""
	}
	if pod != "agent" {
		return ""
	}
	tag := authz.NxtRouteLookup(uid, host)
	s.Debugf("aaa: Route lookup for user %s to host %s : result %s\n", uid, host, tag)
	return tag
}
