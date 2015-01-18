package main

import (
	"log"
	"time"
)

func main() {
	var i int
	for {
		time.Sleep(time.Second * 5)
		log.Println("logging", i)
		i++
	}
}
