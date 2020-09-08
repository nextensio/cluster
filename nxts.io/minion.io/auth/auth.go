/*
 * auth.go - mock library used by minion python code
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package auth

import (
    "fmt"
    "time"
)

func AuthInit(ns string) int { 
    fmt.Printf("auth initialised in namespace %v\n", ns)
    return 0
}

func UsrJoin(pod string, id string) {
    fmt.Printf("user joined %v of type %v\n", id, pod)
}

func UsrLeave(pod string, id string) {
    fmt.Printf("user left %v of type %v\n", id, pod)
}

func GetUsrAttr(id string) string {
    fmt.Println(id)
    usrAttr := "{ dept: computer-science, team: blue }"
    return usrAttr
}

func AccessOk(id string, attr string) bool {
    fmt.Println(id)
    fmt.Println(attr)
  
    return true
}

func AuthStart(ns string) {
    AuthInit(ns)
    fmt.Println("running background task")
    for {
        fmt.Println(time.Now().Format(time.RFC3339))
        time.Sleep(60 * time.Second)
    }
}
