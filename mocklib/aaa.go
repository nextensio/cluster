/*
 * aaa.go - mock library used by minion python code
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package main

import "C"

import (
	"fmt"
	"io/ioutil"
	"strings"
	"time"
)

var Stop bool

//export AaaInit
func AaaInit(ns string, uri string) int {
	fmt.Printf("aaa initialised in namespace %v with uri %v\n", ns, uri)
	return 0
}

//export UsrAllowed
func UsrAllowed(id string) bool {
	fmt.Printf("user allowed %v\n", id)

	return true
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
func GetUsrAttr(id string) *C.char {
	fmt.Println(id)
	dat, _ := ioutil.ReadFile("./dat")
	str := string(dat)
	str = strings.TrimSuffix(str, "\n")
	usrAttr := C.CString(str)
	return usrAttr
}

//export AccessOk
func AccessOk(id string, attr string) bool {
	fmt.Println(id)
	fmt.Println(attr)

	return true
}

//export StopTask
func StopTask() {
	Stop = true
	fmt.Println("got signal to stop")
}

//export RunTask
func RunTask() {
	Stop = false
	fmt.Println("running background task")
	for {
		if Stop == true {
			fmt.Println("exiting task")
			return
		}
		fmt.Println(time.Now().Format(time.RFC3339))
		time.Sleep(60 * time.Second)
	}
}

func main() {}
