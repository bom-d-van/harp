package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// TODOs
// 	rollback
// 	snapshot

// PRINCIPLES
// KISS
// BC (Being Convinent: all things in one place)

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

	Servers map[string][]*Server
}

type App struct {
	Name       string
	ImportPath string
	Files      []string

	Args []string
	Envs map[string]string

	BuildCmd string

	KillSig string

	// TODO: could override default deploy script for out-of-band deploy
	DeployScript string
}

var (
	// verbose   bool
	configPath string
	debugf     bool
	noBuild    bool
	noUpload   bool
	noDeploy   bool
	noFiles    bool
	toTailLog  bool
	script     string
	migration  string

	// TODO: can specify a single server, instead of the whole server set
	server     string
	serverSet  string
	serverSets []string
	help       bool
	versionf   bool

	allf bool

	cfg     Config
	GoPaths = strings.Split(os.Getenv("GOPATH"), ":")
	GoPath  = GoPaths[0]
)

func main() {
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
	flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")
	flag.BoolVar(&allf, "all", false, "execute action on all server")

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

	if args[0] == "init" {
		initHarp()
		return
	}

	cfg = parseCfg(configPath)
	servers := retrieveServers()

	switch args[0] {
	case "deploy":
		deploy(servers)
	case "migrate":
		// TODO: could specify to run on all servers
		migrations := retrieveMigrations(args[1:])
		// var server = cfg.Servers[serverSets[0]][0]
		migrate(servers, migrations)
	case "info":
		inspect(servers)
	case "log":
		toTailLog = true
	case "restart":
		noBuild = true
		noUpload = true
		deploy(servers)
	}

	if toTailLog {
		tailLog(servers)
	}
}

func deploy(servers []*Server) {
	info := getInfo()
	if !noBuild {
		fmt.Println("building")
		build()
	}

	var wg sync.WaitGroup
	// for _, set := range serverSets {
	// 	for _, server := range cfg.Servers[set] {
	for _, server := range servers {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()
			if !noUpload {
				fmt.Printf("uploading: [%s] %s\n", server.set, server)
				server.upload(info)
			}

			if !noDeploy {
				fmt.Printf("deploying: [%s] %s\n", server.set, server)
				server.deploy()
			}
		}(server)
	}
	// }
	wg.Wait()
}

// TODO: use buffer
func inspect(servers []*Server) {
	var wg sync.WaitGroup
	// for _, set := range servers {
	for _, serv := range servers {
		wg.Add(1)
		go func(serv *Server) {
			defer wg.Done()
			session := serv.getSession()
			output, err := session.CombinedOutput(fmt.Sprintf("cat %s/src/%s/harp-build.info", serv.getGoPath(), cfg.App.ImportPath))
			if err != nil {
				exitf("failed to cat %s.info on %s: %s(%s)", cfg.App.Name, serv, err, string(output))
			}
			fmt.Println("=====", serv.String())
			fmt.Println(string(output))
		}(serv)
	}
	// }
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

	if cfg.App.KillSig == "" {
		cfg.App.KillSig = "KILL"
	}

	for k, set := range cfg.Servers {
		for _, s := range set {
			s.set = k
			if s.Port == "" {
				s.Port = ":22"
			}
		}
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
    migrate Run migrations on server (e.g. harp -s prod migrate path/to/my_migration.go).
    info    Print build info of servers (e.g. harp -s prod info).
    log     Print real time logs of application (e.g. harp -s prod log).
    restart Restart application (e.g. harp -s prod restart).
    init    Initialize a harp.json file.
options:`)
	flag.PrintDefaults()
}

func retrieveServers() []*Server {
	var serverSets []string
	for _, set := range strings.Split(serverSet, ",") {
		set = strings.TrimSpace(set)
		if set == "" {
			continue
		}
		serverSets = append(serverSets, set)
	}

	var servers []string
	for _, server := range strings.Split(server, ",") {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		servers = append(servers, server)
	}

	if allf {
		for set, _ := range cfg.Servers {
			serverSets = append(serverSets, set)
		}
	}

	if server == "" && serverSet == "" {
		println("please specify servers or server sets to deploy (-s or -server).")
		println("specify -all flag to execute the action on all servers.")
		os.Exit(1)
	}

	var targetServers []*Server
	for _, set := range serverSets {
		servers, ok := cfg.Servers[set]
		if !ok {
			var existings []string
			for s, _ := range cfg.Servers {
				existings = append(existings, s)
			}
			sort.Sort(sort.StringSlice(existings))
			fmt.Printf("server set doesn't exist: %s (%s)\n", set, strings.Join(existings, ", "))
			os.Exit(1)
		}
		targetServers = append(targetServers, servers...)
	}

serversLoop:
	for _, server := range servers {
		for _, set := range cfg.Servers {
			for _, s := range set {
				if server == s.String() {
					targetServers = append(targetServers, s)
					continue serversLoop
				}
			}
		}
		println("can't find server:", server)
		os.Exit(1)
	}

	return targetServers
}

func initHarp() {
	if _, err := os.Stat("harp.json"); err == nil {
		println("harp.json exists")
		os.Exit(1)
	}
	file, err := os.Create("harp.json")
	if err != nil {
		panic(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	gopath := filepath.Join(filepath.SplitList(os.Getenv("GOPATH"))[0], "src")
	importpath := strings.Replace(wd, gopath+"/", "", 1)
	file.WriteString(fmt.Sprintf(`{
	"goos": "linux",
	"goarch": "amd64",
	"app": {
		"name":       "app",
		"importpath": "%s",
		"envs": {},
		"files":      [
			"%s"
		]
	},
	"servers": {
		"prod": [{
			"gopath": "/home/app",
			"user": "app",
			"host": "",
			"envs": {},
			"port": ":22"
		}]
	}
}`, importpath, importpath))
}
