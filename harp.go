package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// TODOs
// 	rollback
// 	snapshot

// PRINCIPLES
// KISS
// Convinent first, put things together

// TODO: put everything inside app path
// local
// 	pwd/tmp/harp
// 	pwd/migration
//
// server
// 	$GOPATH/bin
// 	$GOPATH/src
// 	$HOME/harp/$APP/build.$num.tar.gz
// 	$HOME/harp/$APP/pid
// 	$HOME/harp/$APP/log
// 	$HOME/harp/$APP/migration.tar.gz
// 	$HOME/harp/$APP/script

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

	// TODO
	Args []string
	Envs map[string]string

	BuildCmd string

	// TODO: could override default deploy script for out-of-band deploy
	DeployScript string
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
	versionf   bool

	cfg    Config
	GoPath = os.Getenv("GOPATH")
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "c", "harp.json", "config file path")
	flag.BoolVar(&debugf, "debug", false, "print debug info")
	// flag.BoolVar(&verbose, "v", false, "verbose")

	flag.BoolVar(&noBuild, "nb", false, "no build")
	flag.BoolVar(&noUpload, "nu", false, "no upload")
	flag.BoolVar(&noDeploy, "nd", false, "no deploy")
	flag.BoolVar(&noFiles, "nf", false, "no files")

	flag.BoolVar(&toTailLog, "log", false, "tail log after deploy")

	flag.BoolVar(&help, "help", false, "print helps")
	flag.BoolVar(&help, "h", false, "print helps")
	flag.BoolVar(&versionf, "v", false, "print version num")
	flag.BoolVar(&versionf, "version", false, "print version num")

	flag.StringVar(&script, "scripts", "", "scripts to build and run on server")

	flag.StringVar(&serverSet, "s", "", "specify server sets to deploy, multiple sets are split by comma")
	flag.StringVar(&serverSet, "server-set", "", "specify server sets to deploy, multiple sets are split by comma")

	flag.StringVar(&migration, "m", "", "specify migrations to run on server, multiple migrations are split by comma")
	// flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")
	flag.Parse()

	if versionf {
		fmt.Printf("0.1.%d\n", version)
		return
	}

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

	for _, set := range serverSets {
		if _, ok := cfg.Servers[set]; !ok {
			fmt.Println("server set doesn't exist:", set)
			os.Exit(1)
		}
	}

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
		toTailLog = true
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
