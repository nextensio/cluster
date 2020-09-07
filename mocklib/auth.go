/*
 * auth.go - mock library used by minion python code
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package main

import "C"

import (
    "fmt"
)

//export AuthInit
func AuthInit(ns string, pod string) int { 
    fmt.Printf("auth initialised in namespace %v\n", ns)
    return 0
}

//export UsrJoin
func UsrJoin(pod string, id string) {
    fmt.Printf("user joined %v of type %v\n", id, pod)
}

//export UsrLeave
func UsrLeave(pod string, id string) {
    fmt.Printf("user left %v of type %v\n", id, pod)
}

//export GetUsrAttr
func GetUsrAttr(id string) string {
    fmt.Println(id)
    usrAttr := "{ dept: computer-science, team: blue }"
    return usrAttr
}

//export AccessOk
func AccessOk(id string, attr string) bool {
    fmt.Println(id)
    fmt.Println(attr)
  
    return true
}

//export RunTask
func RunTask() {
    fmt.Println("running background task")
    for {
    }
}

func main() {}
