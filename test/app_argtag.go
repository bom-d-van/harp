// +build argtag

package main

import (
	"log"
	"time"
)

func init() {
	log.Println("message from arg-tag")
	go func() {
		for {
			time.Sleep(time.Second * 5)
			log.Println("message from arg-tag")
		}
	}()
}
