/*
 * auth.go - auth package for interfaceing with opa library
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package auth

import (
    "time"
    "go.uber.org/zap"
)

func AuthInit(ns string, s *zap.SugaredLogger) int { 
    s.Debugf("auth: initialised in namespace %v\n", ns)
    return 0
}

func UsrJoin(pod string, id string, s *zap.SugaredLogger) {
    s.Debugf("auth: user joined %v of type %v\n", id, pod)
}

func UsrLeave(pod string, id string, s *zap.SugaredLogger) {
    s.Debugf("auth: user left %v of type %v\n", id, pod)
}

func GetUsrAttr(pod string, id string, s *zap.SugaredLogger) (string, bool) {
    s.Debugf("auth: usr attr %v of type %v\n", id, pod)
    if pod == "controller" {
        return "", false
    }
    usrAttr := "{ dept: computer-science, team: blue }"
    return usrAttr, true
}

func AccessOk(pod string, id string, attr string, s *zap.SugaredLogger) bool {
    s.Debugf("auth: access %v of type %v with attr %v\n", id, pod, attr)
    if pod == "agent" {
        return true
    }
    
    return true
}

func AuthStart(ns string, s *zap.SugaredLogger) {
    AuthInit(ns, s)
    s.Debug("auth: running background task")
    for {
        s.Debugf("auth: %v\n", time.Now().Format(time.RFC3339))
        time.Sleep(60 * time.Second)
    }
}
