package main

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// TODO: put logs from different servers into a buffer and print one at at time
func tailLog(servers []*Server) {
	// for _, set := range serverSets {
	for _, serv := range servers {
		go func(serv *Server) {
			session := serv.getSession()

			// TODO: refactor
			{
				r, err := session.StdoutPipe()
				if err != nil {
					exitf("failed to get stdoutPipe: %s", err)
				}
				go io.Copy(os.Stdout, r)
			}
			{
				r, err := session.StderrPipe()
				if err != nil {
					exitf("failed to get StderrPipe: %s", err)
				}
				go io.Copy(os.Stderr, r)
			}

			if err := session.Start(fmt.Sprintf("tail -f -n 20 harp/%s/app.log", cfg.App.Name)); err != nil {
				exitf("tail -f harp/%s/app.log error: %s", cfg.App, err)
			}
		}(serv)
	}
	// }

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}
