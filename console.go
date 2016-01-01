package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"

	"github.com/chzyer/readline"
)

func startConsole(servers []*Server) {
	rl, err := readline.New("> ")
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	type output struct {
		serv   *Server
		output string
	}

	isTerminal := readline.IsTerminal(int(os.Stdin.Fd()))
	for {
		var line string
		var err error
		if isTerminal {
			line, err = rl.Readline()
		} else {
			lineb, er := ioutil.ReadAll(os.Stdin)
			line, err = string(lineb), er
		}
		if err != nil { // io.EOF
			break
		}

		outputc := make(chan output)
		var mutex sync.Mutex
		var count int
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			for {
				mutex.Lock()
				output := <-outputc
				prompt := output.serv.prompt()
				if isTerminal {
					prompt += " " + line
				}
				if count > 0 && strings.TrimSpace(output.output) != "" {
					prompt = "\n" + prompt
				}
				fmt.Println(prompt)
				if strings.TrimSpace(output.output) != "" {
					fmt.Printf(output.output)
				}
				if count++; count >= len(servers) {
					break
				}
				mutex.Unlock()
			}
			wg.Done()
		}()

		for _, serv := range servers {
			wg.Add(1)
			go func(serv *Server) {
				outputc <- output{serv: serv, output: serv.exec(line)}
				wg.Done()
			}(serv)
		}
		wg.Wait()

		if !isTerminal {
			return
		}
	}
}
