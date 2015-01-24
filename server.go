package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"

	"github.com/cheggaaa/pb"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Server struct {
	Env    []string // key=value
	GoPath string
	LogDir string
	PIDDir string

	User string
	Host string
	Port string

	client *ssh.Client
}

func (s Server) upload() {
	if verbose {
		log.Println("upload builds.tar.gz to server")
	}

	src, err := os.OpenFile("tmp/builds.tar.gz", os.O_RDONLY, 0644)
	if err != nil {
		exitf("failed to open tmp/builds.tar.gz: %s", err)
	}
	defer func() { src.Close() }()

	fi, err := src.Stat()
	if err != nil {
		exitf("failed to retrieve file info of %s: %s", src.Name(), err)
	}

	session := s.getSession()
	defer session.Close()

	go func() {
		dst, err := session.StdinPipe()
		if err != nil {
			exitf("failed to get StdinPipe: %s", err)
		}
		defer dst.Close()

		bar := pb.New(int(fi.Size())).SetUnits(pb.U_BYTES)
		bar.Start()
		defer bar.Finish()
		dstw := io.MultiWriter(bar, dst)

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "tmp/builds.tar.gz")
		if err != nil {
			exitf("failed to open builds.tar.gz: %s", err)
		}
		_, err = io.Copy(dstw, src)
		if err != nil {
			exitf("failed to upload builds.tar.gz: %s", err)
		}
		_, err = fmt.Fprint(dst, "\x00")
		if err != nil {
			exitf("failed to close builds.tar.gz: %s", err)
		}
	}()

	if output, err := session.CombinedOutput("/usr/bin/scp -qrt ./"); err != nil {
		exitf("Failed to run: %s %s", string(output), err)
	}
}

func (s Server) deploy() {
	var logs []string
	var script string

	if cfg.Hooks.Deploy.Before != "" {
		before, err := ioutil.ReadFile(cfg.Hooks.Deploy.Before)
		if err != nil {
			exitf("failed to read deploy before hook script: %s", err)
		}
		script += string(before)
		script += "\n"
	}

	app := cfg.App
	var log = fmt.Sprintf("/home/app/log/%s.log", app.Name)
	var pid = fmt.Sprintf("/home/app/pid/%s.pid", app.Name)
	logs = append(logs, log)
	script += fmt.Sprintf(`mkdir -p log
mkdir -p pid
if [[ -f %[1]s ]]; then
	target=$(cat %[1]s);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
fi
tar mxf tmp/builds.tar.gz
touch %s
`, pid, log)
	var path = s.GoPath
	if path == "" {
		session := s.getSession()
		output, _ := session.CombinedOutput("echo $GOPATH")
		session.Close()
		path = strings.TrimSpace(string(output))
	}
	if path == "" {
		session := s.getSession()
		output, _ := session.CombinedOutput("echo $HOME")
		session.Close()
		path = strings.TrimSpace(string(output))
	}
	script += fmt.Sprintf("cd %s/src/%s\n", path, app.ImportPath)
	script += fmt.Sprintf("GOPATH=%s nohup %s/bin/%s >> %s 2>&1 &\n", s.GoPath, path, app.Name, log)
	script += fmt.Sprintf("echo $! > %s\n", pid)

	if cfg.Hooks.Deploy.After != "" {
		after, err := ioutil.ReadFile(cfg.Hooks.Deploy.After)
		if err != nil {
			exitf("failed to read deploy after hook script: %s", err)
		}
		script += string(after)
		script += "\n"
	}

	if debugf {
		fmt.Printf("%s", script)
	}

	session := s.getSession()
	defer session.Close()

	var output []byte
	output, err := session.CombinedOutput(script)
	if err != nil {
		exitf("failed to exec %s: %s %s", script, string(output), err)
	}
}

func (s *Server) getSession() *ssh.Session {
	if s.client == nil {
		sock, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			exitf("failed to dial unix SSH_AUTH_SOCK: %s", err)
		}
		signers, err := agent.NewClient(sock).Signers()
		if err != nil {
			exitf("failed to retrieve signers: %s", err)
		}
		auths := []ssh.AuthMethod{ssh.PublicKeys(signers...)}
		config := &ssh.ClientConfig{
			User: "app",
			Auth: auths,
		}

		s.client, err = ssh.Dial("tcp", s.Host+s.Port, config)
		if err != nil {
			exitf("failed to dial: %s", err)
		}
	}

	session, err := s.client.NewSession()
	if err != nil {
		exitf("failed to get session to server %s@%s:%s: %s", s.User, s.Host, s.Port, err)
	}

	return session
}

func (s Server) String() string {
	return fmt.Sprintf("%s@%s%s", s.User, s.Host, s.Port)
}

func (s Server) initSetUp() {
	session := s.getSession()
	session.CombinedOutput("mkdir")
}
