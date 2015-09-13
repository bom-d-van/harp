// +build filetag

package main

import (
	"log"
	"time"
)

func init() {
	go func() {
		log.Println("message from file-tag")
		for {
			time.Sleep(time.Second * 5)
			log.Println("message from file-tag")
		}
	}()
}
