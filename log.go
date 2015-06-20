package main

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"time"
)

// TODO: put logs from different servers into a buffer and print one at at time
func tailLog(servers []*Server, beginLineNum int) {
	output := make(chan output)
	go outputLogs(output)
	for _, serv := range servers {
		go func(serv *Server) {
			serv.initPathes()
			session := serv.getSession()

			logger := NewLogger(output, fmt.Sprintf("========================\n%s", serv))
			session.Stdout = logger
			session.Stderr = logger

			if err := session.Start(fmt.Sprintf("tail -f -n %d %s/harp/%s/app.log", beginLineNum, serv.Home, cfg.App.Name)); err != nil {
				exitf("tail -f harp/%s/app.log error: %s", cfg.App, err)
			}
		}(serv)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}

type Logger struct {
	// data []byte
	prefix []byte
	mutex  sync.Mutex
	Buffer bytes.Buffer

	output chan output
}

type output struct {
	data   []byte
	prefix []byte
}

func NewLogger(outputc chan output, prefix string) (l *Logger) {
	l = &Logger{}
	l.output = outputc
	l.prefix = []byte(prefix)
	ticker := time.NewTicker(time.Second * 3)
	go func() {
		for {
			<-ticker.C
			l.mutex.Lock()
			data := l.Buffer.Bytes()
			l.Buffer.Reset()
			l.mutex.Unlock()
			go func() {
				if len(data) == 0 {
					return
				}
				l.output <- output{data, l.prefix}
			}()
		}
	}()

	return l
}

func (l *Logger) Write(data []byte) (n int, err error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	n, err = l.Buffer.Write(data)
	if l.Buffer.Len() < 1024 {
		return
	}

	data = l.Buffer.Bytes()
	l.Buffer.Reset()
	go func() {
		l.output <- output{data, l.prefix}
	}()

	return
}

func outputLogs(outputc chan output) {
	var old output
	for {
		output := <-outputc
		if !bytes.Equal(output.prefix, old.prefix) {
			log.Print(string(output.prefix))
		}
		log.Print(string(output.data))
		old = output
	}
}
