package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/cheggaaa/pb"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// TODOs
// 	rollback
// 	snapshot

// local
// 	pwd/tmp/harp
// 	pwd/migration
//
// server
// 	$GOPATH/bin
// 	$GOPATH/src
// 	$GOPATH/pid
// 	$GOPATH/log
// 	$GOPATH/migration/$APP
// 	$GOPATH/script

// use cases 1: in pwd: go build -> upload -> run

type Config struct {
	GOOS, GOARCH string

	Rollback int
	// Name     string

	// Pkgs                      map[string]string
	// Daemons                   []string
	// Output                    string

	App App

	// Hook struct{ BeforeDeploy, AfterDeploy string }

	// ServerGoPath, LocalGoPath string

	// Files                     []Files
	// App                       string
	// Log                       string

	Servers map[string][]Server

	// MigrationDir string
}

type App struct {
	Name       string
	ImportPath string
	Files      []string
	Args       []string

	BuildCmd    string
	BuildScript string
}

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

// type Files struct {
// 	// Absolute    bool
// 	// InBin bool
// 	Src string
// 	Dst string
// }

var (
	verbose  bool
	noBuild  bool
	noUpload bool
	noDeploy bool
	// tailLog  bool
	script string

	serverSet  string
	serverSets []string

	cfg    Config
	GoPath = os.Getenv("GOPATH")
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "c", "harp.json", "config file path")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.BoolVar(&noBuild, "nb", false, "no build")
	flag.BoolVar(&noUpload, "nu", false, "no upload")
	flag.BoolVar(&noDeploy, "nd", false, "no deploy")
	// flag.BoolVar(&tailLog, "log", false, "tail log after deploy")
	flag.StringVar(&script, "scripts", "", "scripts to build and run on server")
	flag.StringVar(&serverSet, "s", "", "specify server sets to deploy, multiple sets are split by comma")
	flag.StringVar(&serverSet, "server-set", "", "specify server sets to deploy, multiple sets are split by comma")
	// flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")
	flag.Parse()

	var serverSets []string
	if serverSet == "" {
		fmt.Println("must specify server set with -server-set or -s")
		os.Exit(1)
	}

	serverSets = strings.Split(serverSet, ",")
	for i, server := range serverSets {
		serverSets[i] = strings.TrimSpace(server)
	}

	cfg = parseCfg(configPath)

	args := flag.Args()
	if len(args) == 0 {
		flag.PrintDefaults()
		return
	}

	switch args[0] {
	case "deploy":
		deploy(serverSets)
	case "migration":
		// TODO
	case "info":
		inspect(serverSets)
	case "log":
		tailLog(serverSets)
	}

	// if tailLog {
	// script += "tail -f " + strings.Join(logs, " ")
	// }
}

func deploy(serverSets []string) {
	info := getInfo()
	if !noBuild {
		println("build")
		build()
	}

	if !noUpload {
		println("bundle")
		bundle(info)
	}

	var wg sync.WaitGroup
	for _, set := range serverSets {
		for _, server := range cfg.Servers[set] {
			wg.Add(1)
			go func(set string, server Server) {
				defer wg.Done()
				if server.Port == "" {
					server.Port = ":22"
				}

				if !noUpload {
					fmt.Printf("%s: %s@%s%s upload\n", set, server.User, server.Host, server.Port)
					server.upload()
				}

				if !noDeploy {
					fmt.Printf("%s: %s@%s%s deploy\n", set, server.User, server.Host, server.Port)
					server.deploy()
				}
			}(set, server)
		}
	}
	wg.Wait()
}

// TODO: put logs from different servers into a buffer and print one at at time
func tailLog(serverSets []string) {
	for _, set := range serverSets {
		for _, serv := range cfg.Servers[set] {
			go func(set string, serv Server) {
				session := serv.getSession()
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
				if err := session.Start(fmt.Sprintf("tail -f log/%s.log", cfg.App.Name)); err != nil {
					exitf("tail -f log/%s.log error: %s", cfg.App, err)
				}
			}(set, serv)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}

// TODO: use buffer
func inspect(serverSets []string) {
	var wg sync.WaitGroup
	for _, set := range serverSets {
		for _, serv := range cfg.Servers[set] {
			wg.Add(1)
			go func(set string, serv Server) {
				defer wg.Done()
				session := serv.getSession()
				output, err := session.CombinedOutput(fmt.Sprintf("cat %s.info", cfg.App.Name))
				if err != nil {
					exitf("failed to cat %s.info on %s: %s(%s)", cfg.App.Name, serv, err, string(output))
				}
				fmt.Println("=====", serv.String())
				fmt.Println(string(output))
			}(set, serv)
		}
	}
	wg.Wait()
}

func parseCfg(configPath string) (cfg Config) {
	cfgFile, err := os.OpenFile(configPath, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		exitf("failed to read config: %s", err)
	}
	err = json.NewDecoder(cfgFile).Decode(&cfg)
	if err != nil {
		exitf("failed to parse config: %s", err)
	}

	return
}

func getInfo() string {
	var info string
	info += "Go Version: " + cmd("go", "version")
	if cfg.GOOS != "" {
		info += "GOOS: " + cfg.GOOS + "\n"
	}
	if cfg.GOARCH != "" {
		info += "GOARCH: " + cfg.GOARCH + "\n"
	}
	if isUsingGit() {
		info += "Git Checksum: " + cmd("git", "rev-parse", "HEAD")
		info += "Composer: " + cmd("git", "config", "user.name")
	}
	info += "Build At: " + time.Now().String()

	return info
}

func isUsingGit() bool {
	_, err := os.Stat(".git")
	return err == nil
}

func cmd(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	// cmd.Dir
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "GOOS="+cfg.GOOS, "GOARCH="+cfg.GOARCH)

	output, err := cmd.CombinedOutput()
	if err != nil {
		exitf("faied to run %s %s: %s(%s)\n", name, args, err, string(output))
	}

	return string(output)
}

// func getClients() (client *ssh.Client) {
// 	sock, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
// 	if err != nil {
// 		exitf("failed to dial unix SSH_AUTH_SOCK: %s", err)
// 	}
// 	signers, err := agent.NewClient(sock).Signers()
// 	if err != nil {
// 		exitf("failed to retrieve signers: %s", err)
// 	}
// 	auths := []ssh.AuthMethod{ssh.PublicKeys(signers...)}
// 	config := &ssh.ClientConfig{
// 		User: "app",
// 		Auth: auths,
// 	}

// 	serv := cfg.Servers["dev"][0]
// 	client, err = ssh.Dial("tcp", serv.Host+serv.Port, config)
// 	if err != nil {
// 		exitf("failed to dial: %s", err)
// 	}

// 	// ftpClient, err = sftp.NewClient(client)
// 	// if err != nil {
// 	// 	exitf("failed to open sftp client: %s", err)
// 	// }

// 	return
// }

func build() {
	// var wg sync.WaitGroup
	// for _, app := range cfg.Apps {
	// wg.Add(1)
	// go func(app App) {
	// defer wg.Done()
	app := cfg.App

	// var cmd = exec.Command("go", "build", "-o", "tmp/"+name, "-tags", "prod", pkg)
	// cmd.Dir = cfg.LocalGoPath + "/src/" + pkg
	// cmd.Env = os.Environ()
	// cmd.Env = append(cmd.Env, "GOOS="+cfg.GOOS, "GOARCH="+cfg.GOARCH)
	var buildCmd = fmt.Sprintf("go build -o tmp/%s %s", app.Name, app.ImportPath)
	if app.BuildCmd != "" {
		buildCmd = app.BuildCmd
	} else if app.BuildScript != "" {
		buildCmd = app.BuildScript
	}
	cmd("sh", "-c", buildCmd)
	// output, err := cmd.CombinedOutput()
	// if err != nil {
	// 	exitf("failed to build %s: %s: %s", pkg, err, string(output))
	// }
	// }(app)
	// }
	// wg.Wait()
}

func bundle(info string) {
	dst, err := os.OpenFile("tmp/builds.tar.gz", os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer dst.Close()
	gzipw := gzip.NewWriter(dst)
	defer gzipw.Close()
	tarw := tar.NewWriter(gzipw)
	defer tarw.Close()

	// for _, app := range cfg.Apps {
	app := cfg.App
	for _, files := range app.Files {
		var path = GoPath + "/src/" + files
		fi, err := os.Stat(path)
		if err != nil {
			exitf("failed to stat %s: %s", path, err)
		}
		if fi.IsDir() {
			filepath.Walk(path, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					exitf("filepath walk error from %s: %s", path, err)
				}
				if fi.IsDir() {
					return nil
				}
				name := strings.TrimPrefix(path, GoPath+"/src/")
				file, err := os.Open(path)
				if err != nil {
					exitf("failed to open file %s: %s", path, err)
				}
				writeToTar(tarw, "src/"+name, file, fi)

				return nil
			})
		} else {
			file, err := os.Open(path)
			if err != nil {
				exitf("failed to open %s: %s", path, err)
			}
			var p string
			p = "src/" + files
			writeToTar(tarw, p, file, fi)
		}
	}

	file, err := os.Open("tmp/" + app.Name)
	if err != nil {
		exitf("failed to open tmp/%s: %s", app.Name, err)
	}
	fi, err := file.Stat()
	if err != nil {
		exitf("failed to stat %s: %s", file.Name(), err)
	}
	writeToTar(tarw, "bin/"+app.Name, file, fi)
	// }

	writeInfoToTar(tarw, info)
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

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "builds.tar.gz")
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

func writeToTar(tarw *tar.Writer, name string, file io.Reader, fi os.FileInfo) {
	header := new(tar.Header)
	header.Name = name
	header.Size = fi.Size()
	header.Mode = int64(fi.Mode())
	header.ModTime = fi.ModTime()

	err := tarw.WriteHeader(header)
	if err != nil {
		exitf("failed to write tar header for %s: %s", name, err)
	}

	_, err = io.Copy(tarw, file)
	if err != nil {
		exitf("failed to write %s into tar file: %s", name, err)
	}
}

func writeInfoToTar(tarw *tar.Writer, info string) {
	header := new(tar.Header)
	header.Name = cfg.App.Name + ".info"
	header.Size = int64(len(info))
	header.Mode = int64(0644)
	header.ModTime = time.Now()

	err := tarw.WriteHeader(header)
	if err != nil {
		exitf("failed to write tar header for %s: %s", header.Name, err)
	}

	_, err = tarw.Write([]byte(info))
	if err != nil {
		exitf("failed to write %s into tar file: %s", header.Name, err)
	}
}

func (s Server) deploy() {
	var logs []string
	var script = "mkdir -p log\n"
	script += "mkdir -p pid\n"
	// for _, app := range cfg.Apps {
	app := cfg.App
	var log = fmt.Sprintf("/home/app/log/%s.log", app.Name)
	var pid = fmt.Sprintf("/home/app/pid/%s.pid", app.Name)
	logs = append(logs, log)
	script += fmt.Sprintf(`tar mxf builds.tar.gz
if [[ -f %[1]s ]]; then
	target=$(cat %[1]s);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
fi
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
	script += fmt.Sprintf("nohup %s/bin/%s >> %s 2>&1 &\n", path, app.Name, log)
	script += fmt.Sprintf("echo $! > %s\n", pid)
	// }
	fmt.Printf("%s", script)

	session := s.getSession()
	defer session.Close()

	// stdoutPipe, err := session.StdoutPipe()
	// if err != nil {
	// 	exitf("failed to get StdoutPipe: %s", err)
	// }
	// stderrPipe, err := session.StderrPipe()
	// if err != nil {
	// 	exitf("failed to get StderrPipe: %s", err)
	// }
	// go func() {
	// 	io.Copy(os.Stdout, stdoutPipe)
	// 	io.Copy(os.Stderr, stderrPipe)
	// }()
	var output []byte
	output, err := session.CombinedOutput(script)
	println(string(output))
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

		// serv := cfg.Servers["dev"][0]

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

func runCmd(sshc *ssh.Client, cmd string) (output []byte, err error) {
	session, err := sshc.NewSession()
	if err != nil {
		exitf("failed to create session: %s", err)
	}
	defer session.Close()

	output, err = session.CombinedOutput(cmd)
	println(string(output))
	if err != nil {
		exitf("failed to exec %s: %s %s", cmd, string(output), err)
	}

	return
}

func exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Printf(format, args...)
	debug.PrintStack()
	os.Exit(1)
}
