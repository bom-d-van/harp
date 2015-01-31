// +build ignore

package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println("AppEnv=" + os.Getenv("AppEnv"))
	fmt.Println("Args:", os.Args[1:])
	for i := 0; i < 5; i++ {
		fmt.Println("logging", i)
		time.Sleep(time.Second * time.Duration(i))
	}
}
