package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	var i int
	log.SetPrefix(fmt.Sprint(os.Getpid()) + " ")
	for {
		time.Sleep(time.Second * 5)
		log.Println("logging", i)
		i++
	}
}
