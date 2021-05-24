package aaa

import (
	"go.uber.org/zap"
	"minion.io/authz"
)

func AaaStart(ns string, pod string, gateway string, uri string, s *zap.SugaredLogger, disconnectCb func(string, *zap.SugaredLogger)) {
	authz.NxtAaaInit(ns, pod, gateway, uri, s, disconnectCb)
}

func UsrAllowed(which string, id string, cluster string, podname string, s *zap.SugaredLogger) bool {

	val := authz.NxtUsrAllowed(which, id, cluster, podname)
	s.Debugf("aaa: user allowed %v, %v : result %v\n", which, id, val)
	return val
}

func UsrLeave(which string, id string, s *zap.SugaredLogger) {
	s.Debugf("aaa: user left %v of type %v\n", id, which)
	authz.NxtUsrLeave(which, id)
}

func GetUsrAttr(which string, id string, s *zap.SugaredLogger) (string, bool) {
	return authz.NxtGetUsrAttr(which, id)
}

func AccessOk(which string, id string, attr string, s *zap.SugaredLogger) bool {
	if !authz.NxtAccessOk(which, id, attr) {
		s.Debugf("aaa: user %v with attr %v denied access\n", id, attr)
		return false
	}

	return true
}

func RouteLookup(which string, uid string, host string, s *zap.SugaredLogger) string {
	tag := authz.NxtRouteLookup(which, uid, host)
	if tag != "" {
		s.Debugf("aaa: Route lookup for user %s to host %s : result %s\n", uid, host, tag)
	}
	return tag
}
