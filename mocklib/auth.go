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
func AuthInit() int { 
    fmt.Println("auth initialised")
    return 0
}

//export GetUsrAttr
func GetUsrAttr (id string) string {
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

func main() {}
