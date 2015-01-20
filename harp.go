package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
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

// TODO: put everything inside app path
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

	// TODO
	Rollback int

	// TODO: multiple instances support
	// TODO: multiple apps support
	App App

	// TODO: migration and flag support (-after and -before)
	Hooks struct {
		Deploy struct {
			Before, After string
		}
	}

	Servers map[string][]Server
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

var (
	verbose   bool
	debugf    bool
	noBuild   bool
	noUpload  bool
	noDeploy  bool
	noFiles   bool
	toTailLog bool
	script    string
	migration string

	// TODO: can specify a single server, instead of the whole server set
	serverSet  string
	serverSets []string
	help       bool

	cfg    Config
	GoPath = os.Getenv("GOPATH")
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "c", "harp.json", "config file path")
	flag.BoolVar(&debugf, "debug", false, "print debug info")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.BoolVar(&noBuild, "nb", false, "no build")
	flag.BoolVar(&noUpload, "nu", false, "no upload")
	flag.BoolVar(&noDeploy, "nd", false, "no deploy")
	flag.BoolVar(&noFiles, "nf", false, "no files")
	flag.BoolVar(&help, "help", false, "print helps")
	flag.BoolVar(&help, "h", false, "print helps")
	flag.BoolVar(&toTailLog, "log", false, "tail log after deploy")
	flag.StringVar(&script, "scripts", "", "scripts to build and run on server")
	flag.StringVar(&serverSet, "s", "", "specify server sets to deploy, multiple sets are split by comma")
	flag.StringVar(&serverSet, "server-set", "", "specify server sets to deploy, multiple sets are split by comma")
	flag.StringVar(&migration, "m", "", "specify migrations to run on server, multiple migrations are split by comma")
	// flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 || help {
		printUsage()
		return
	}

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

	switch args[0] {
	case "deploy":
		deploy(serverSets)
	case "migrate":
		// TODO: could specify to run on all servers
		var server = cfg.Servers[serverSets[0]][0]
		migrate(server, getList(migration))
	case "info":
		inspect(serverSets)
	case "log":
		tailLog(serverSets)
	case "restart":
		noBuild = true
		noUpload = true
		deploy(serverSets)
	}

	if toTailLog {
		tailLog(serverSets)
	}
}

func getList(str string) (strs []string) {
	for _, str := range strings.Split(str, ",") {
		strs = append(strs, strings.TrimSpace(str))
	}

	return
}

func deploy(serverSets []string) {
	info := getInfo()
	if !noBuild {
		fmt.Println("building")
		build()
	}

	if !noUpload {
		fmt.Println("bundling")
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
					fmt.Printf("uploading: [%s] %s\n", set, server)
					server.upload()
				}

				if !noDeploy {
					fmt.Printf("deploying: [%s] %s\n", set, server)
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

				if err := session.Start(fmt.Sprintf("tail -f -n 20 log/%s.log", cfg.App.Name)); err != nil {
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
			fmt.Printf("Config %s doesn't exist or is unspecified.\nTo specify with flag -c (e.g. -c harp.json)\n", configPath)
			os.Exit(1)
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

func build() {
	app := cfg.App

	var buildCmd = fmt.Sprintf("go build -a -v -o tmp/%s %s", app.Name, app.ImportPath)
	if app.BuildCmd != "" {
		buildCmd = app.BuildCmd
	} else if app.BuildScript != "" {
		buildCmd = app.BuildScript
	}
	if debugf {
		println("build cmd:", buildCmd)
	}
	output := cmd("sh", "-c", buildCmd)
	if debugf {
		print(output)
	}
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

	app := cfg.App
	if noFiles {
		goto bundleBinary
	}

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

bundleBinary:
	if !noBuild {
		file, err := os.Open("tmp/" + app.Name)
		if err != nil {
			exitf("failed to open tmp/%s: %s", app.Name, err)
		}
		fi, err := file.Stat()
		if err != nil {
			exitf("failed to stat %s: %s", file.Name(), err)
		}
		writeToTar(tarw, "bin/"+app.Name, file, fi)
	}

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
tar mxf builds.tar.gz
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

func printUsage() {
	fmt.Println(`harp is a go application deployment tool.
usage:
    harp [options] [action]
actions:
    deploy  Deploy your application (e.g. harp -s prod deploy).
    migrate Run migrations on server (e.g. harp -s prod -m migration/reset_info.go migrate).
    info    Print build info of servers (e.g. harp -s prod info).
    log     Print real time logs of application (e.g. harp -s prod log).
    restart Restart application (e.g. harp -s prod restart).
options:`)
	flag.PrintDefaults()
}
