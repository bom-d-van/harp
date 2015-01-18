// +build ignore

package main

import (
	"fmt"
	"time"
)

func main() {
	for i := 0; i < 5; i++ {
		fmt.Println("logging", i)
		time.Sleep(time.Second * time.Duration(i))
	}
}
