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

	s.initSetUp()

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

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "build.tar.gz")
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

	if output, err := session.CombinedOutput("/usr/bin/scp -qrt harp/" + cfg.App.Name); err != nil {
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
	log := fmt.Sprintf("$HOME/harp/%s/app.log", app.Name)
	pid := fmt.Sprintf("$HOME/harp/%s/app.pid", app.Name)
	logs = append(logs, log)
	script += fmt.Sprintf(`if [[ -f %[1]s ]]; then
	target=$(cat %[1]s);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
fi
tar mxf harp/%[3]s/build.tar.gz
touch %[2]s
`, pid, log, app.Name)
	path := s.getGoPath()
	envs := "GOPATH=" + s.GoPath
	for k, v := range app.Envs {
		envs += fmt.Sprintf(" %s=%s", k, v)
	}
	args := strings.Join(app.Args, " ")
	script += fmt.Sprintf("cd %s/src/%s\n", path, app.ImportPath)
	script += fmt.Sprintf("%s nohup %s/bin/%s %s >> %s 2>&1 &\n", envs, path, app.Name, args, log)
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

	// TODO: save scripts(s) for starting, restarting, or kill app
}

func (s Server) getGoPath() string {
	var path = s.GoPath
	if path == "" {
		session := s.getSession()
		output, err := session.CombinedOutput("echo $GOPATH")
		if err != nil {
			fmt.Printf("echo $GOPATH on %s error: %s\n", s, err)
		}
		session.Close()
		path = strings.TrimSpace(string(output))
	}
	if path == "" {
		session := s.getSession()
		output, err := session.CombinedOutput("echo $HOME")
		if err != nil {
			fmt.Printf("echo $HOME on %s error: %s\n", s, err)
		}
		session.Close()
		path = strings.TrimSpace(string(output))
	}

	return path
}

func (s *Server) getSession() *ssh.Session {
	if s.client == nil {
		s.initClient()
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

func (s *Server) initClient() {
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

func (s *Server) initSetUp() {
	if s.client == nil {
		s.initClient()
	}
	runCmd(s.client, "mkdir -p harp/"+cfg.App.Name)
}
