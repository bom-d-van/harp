package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	var i int
	log.SetPrefix(fmt.Sprintf("%d %d: ", os.Getpid(), version))
	log.Println("running", i)
	for {
		time.Sleep(time.Second * 5)
		i++
		log.Println("running", i)
	}
}
