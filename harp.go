package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
)

// TODOs
// 	rollback
// 	snapshot
// 	tmux support for long migrations

// PRINCIPLES
// KISS
// BC (Being Convinent: all things in one place)

// TODO: put everything inside app path
// local
// 	pwd/.harp/files
// 	pwd/.harp/migration
//
// server
// 	$GOPATH/bin
// 	$GOPATH/src
// 	$HOME/harp/$APP/build.$num.tar.gz
// 	$HOME/harp/$APP/pid
// 	$HOME/harp/$APP/log
// 	$HOME/harp/$APP/migration.tar.gz
// 	$HOME/harp/$APP/script

func init() {
	if debugf {
		log.SetFlags(log.Lshortfile)
	} else {
		log.SetFlags(0)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			cleanCaches()
		}
	}()
}

type Config struct {
	GOOS, GOARCH string

	// TODO
	NoRollback    bool
	RollbackCount int

	// TODO: multiple instances support
	// TODO: multiple apps support
	App App

	// // TODO: migration and flag support (-after and -before)
	// Hooks struct {
	// 	Deploy struct {
	// 		Before, After string
	// 	}
	// }

	Servers map[string][]*Server
}

type App struct {
	Name       string
	ImportPath string

	DefaultExcludedFiles []string
	Files                []File
	// Files []string

	Args []string
	Envs map[string]string

	BuildCmd string

	KillSig string

	// TODO: could override default deploy script for out-of-band deploy
	DeployScript  string
	RestartScript string
}

var (
	// TODO: move flags into Config
	// verbose   bool
	configPath string
	debugf     bool
	noBuild    bool
	noUpload   bool
	noDeploy   bool
	noFiles    bool
	script     string
	migration  string

	softExclude bool
	keepCache   bool

	toTailLog        bool
	tailBeginLineNum int

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

var tmpDir = ".harp"

func main() {
	if err := os.RemoveAll(tmpDir); err != nil {
		exitf("os.RemoveAll(%s) error: %s", tmpDir, err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		exitf("os.MkdirAll(%s) error: %s", tmpDir, err)
	}
	defer cleanCaches()

	flag.StringVar(&configPath, "c", "harp.json", "config file path")

	flag.BoolVar(&debugf, "debug", false, "print debug info")
	// flag.BoolVar(&verbose, "v", false, "verbose")

	flag.BoolVar(&noBuild, "nb", false, "no build")
	flag.BoolVar(&noBuild, "no-build", false, "no build")

	flag.BoolVar(&noUpload, "nu", false, "no upload")
	flag.BoolVar(&noUpload, "no-upload", false, "no upload")

	flag.BoolVar(&noDeploy, "nd", false, "no deploy")
	flag.BoolVar(&noDeploy, "no-deploy", false, "no deploy")
	flag.BoolVar(&noDeploy, "nr", false, "no run (same as -no-deploy)")
	flag.BoolVar(&noDeploy, "no-run", false, "no run (same as -no-deploy)")

	flag.BoolVar(&noFiles, "nf", false, "no files")
	flag.BoolVar(&noFiles, "no-files", false, "no files")

	flag.BoolVar(&toTailLog, "log", false, "tail log after deploy")
	flag.IntVar(&tailBeginLineNum, "n", 32, "tail log tail localtion line number (tail -n 32)")

	flag.BoolVar(&help, "help", false, "print helps")
	flag.BoolVar(&help, "h", false, "print helps")
	flag.BoolVar(&versionf, "v", false, "print version num")
	flag.BoolVar(&versionf, "version", false, "print version num")

	flag.BoolVar(&softExclude, "soft-exclude", false, "use strings.Contains to exclude files")
	flag.BoolVar(&keepCache, "cache", false, "cache data in .harp")

	// flag.StringVar(&script, "scripts", "", "scripts to build and run on server")

	flag.StringVar(&serverSet, "s", "", "specify server sets to deploy, multiple sets are split by comma")
	flag.StringVar(&serverSet, "server-set", "", "specify server sets to deploy, multiple sets are split by comma")

	flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")

	flag.BoolVar(&allf, "all", false, "execute action on all server")

	flag.StringVar(&migration, "m", "", "specify migrations to run on server, multiple migrations are split by comma")
	// flag.StringVar(&server, "server", "", "specify servers to deploy, multiple servers are split by comma")
	flag.Parse()

	if versionf {
		fmt.Printf("0.3.%d\n", version)
		return
	}

	args := flag.Args()
	if len(args) == 0 || help {
		printUsage()
		return
	}

	switch args[0] {
	case "init":
		initHarp()
		return
	case "clean":
		keepCache = false
		// log.Println("removing .harp")
		cleanCaches()
	}

	cfg = parseCfg(configPath)

	var servers []*Server
	if args[0] != "cross-compile" && args[0] != "xc" {
		servers = retrieveServers()
	}

	switch args[0] {
	case "kill":
		// TODO
		kill(servers)
	case "deploy":
		deploy(servers)
	case "migrate", "run":
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
	case "inspect":
		inspectScript(servers, args[1])
	case "rollback":
		if len(args) == 1 {
			fmt.Println("please specify rollback command or version")
			os.Exit(1)
		}
		if args[1] == "ls" || args[1] == "list" {
			lsRollbackVersions(servers, args[1] == "list")
		} else {
			rollback(servers, strings.TrimSpace(args[1]))
		}
	case "cross-compile", "xc":
		initXC()
	}

	if toTailLog {
		tailLog(servers, tailBeginLineNum)
	}
}

func deploy(servers []*Server) {
	info := getBuildLog()
	if !noBuild {
		log.Println("building")
		build()
	}

	if !noUpload {
		syncFiles()
	}

	var wg sync.WaitGroup
	// for _, set := range serverSets {
	// 	for _, server := range cfg.Servers[set] {
	for _, server := range servers {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()
			if !noUpload {
				log.Printf("uploading: [%s] %s\n", server.Set, server)
				server.upload(info)
			}

			if !noDeploy {
				log.Printf("deploying: [%s] %s\n", server.Set, server)
				server.deploy()
			}
		}(server)
	}
	// }
	wg.Wait()
}

func syncFiles() {
	log.Println("syncing files")
	if err := os.MkdirAll(filepath.Join(tmpDir, "files"), 0755); err != nil {
		exitf("os.MkdirAll(.harp/files) error: %s", err)
	}

	var wg sync.WaitGroup
	wg.Add(len(cfg.App.Files))
	for _, file := range cfg.App.Files {
		go func(f File) {
			defer func() { wg.Done() }()
			var src, gopath string
			for _, gopath = range GoPaths {
				src = filepath.Join(gopath, "src", f.Path)
				if _, err := os.Stat(src); err != nil {
					src = ""
					continue
				}

				break
			}
			if src == "" {
				exitf("failed to find %s from %s", f.Path, GoPaths)
			}

			dst := filepath.Join(tmpDir, "files", strings.Replace(f.Path, "/", "_", -1))
			if fi, err := os.Stat(src); err != nil {
				exitf("os.Stat(%s) error: %s", src, err)
			} else if fi.IsDir() {
				if err := os.Mkdir(dst, 0755); err != nil {
					exitf("os.Mkdir(%s) error: %s", dst, err)
				}
			} else {
				copyFile(dst, src)
			}
			base := filepath.Join(gopath, "src", f.Path)
			err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					exitf("walk %s: %s", path, err)
				} else if path == base {
					return nil
				}

				rel, err := filepath.Rel(base, path)
				if err != nil {
					exitf("fielpath.Rel(%s, %s) error: %s", base, path, err)
				}

				for _, e := range append(cfg.App.DefaultExcludedFiles, f.Excludes...) {
					matched, err := filepath.Match(e, rel)
					if err != nil {
						exitf("filepath.Match(%s, %s) error: %s", e, rel, err)
					}
					if !matched && !softExclude {
						matched = strings.Contains(rel, e)
					}
					if matched {
						if info.IsDir() {
							return filepath.SkipDir
						} else {
							return nil
						}
					}
				}

				if info.IsDir() {
					if err := os.Mkdir(filepath.Join(dst, rel), info.Mode()); err != nil {
						exitf("os.Mkdir(%s) error: %s", filepath.Join(dst, rel), err)
					}
					return nil
				}

				wg.Add(1)
				go func() {
					defer func() { wg.Done() }()
					copyFile(filepath.Join(dst, rel), path)
				}()
				return nil
			})
			if err != nil && err != filepath.SkipDir {
				exitf("walking %s: %s", src, err)
			}
		}(file)
	}

	wg.Wait()
}

// TODO: use buffer
func inspect(servers []*Server) {
	var wg sync.WaitGroup
	for _, serv := range servers {
		wg.Add(1)
		go func(serv *Server) {
			defer wg.Done()
			serv.initPathes()
			session := serv.getSession()
			output, err := session.CombinedOutput(fmt.Sprintf("cat %s/src/%s/harp-build.info", serv.GoPath, cfg.App.ImportPath))
			if err != nil {
				exitf("failed to cat %s.info on %s: %s(%s)", cfg.App.Name, serv, err, string(output))
			}
			fmt.Println("=====", serv.String())
			fmt.Println(string(output))
		}(serv)
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

	if cfg.App.KillSig == "" {
		cfg.App.KillSig = "KILL"
	}

	for k, set := range cfg.Servers {
		for _, s := range set {
			s.Set = k
			if s.Port == "" {
				s.Port = ":22"
			}
		}
	}

	if cfg.RollbackCount == 0 {
		cfg.RollbackCount = 3
	}

	return
}

func getBuildLog() string {
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

	buildCmd := fmt.Sprintf("go build -a -v -o %s %s", filepath.Join(tmpDir, app.Name), app.ImportPath)
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
    deploy   Deploy your application (e.g. harp -s prod deploy).
    run      Run migrations on server (e.g. harp -s prod migrate path/to/my_migration.go).
    kill     Kill server.
    info     Print build info of servers (e.g. harp -s prod info).
    log      Print real time logs of application (e.g. harp -s prod log).
    restart  Restart application (e.g. harp -s prod restart).
    init     Initialize a harp.json file.
    rollback
        ls       List all the current releases.
        $version Rollback to $version.

options:`)
	flag.PrintDefaults()

	fmt.Println(`
examples:
    Deploy:
        harp -s prod -log deploy

    Compile and run a go package or file in server/Migration:
        Simple:
            harp -server app@192.168.59.103:49153 run migration.go

        With env and arguments (behold the quotes):
            harp -server app@192.168.59.103:49153 run "Env1=val Env2=val migration2.go -arg1 val1"

        Multiple migrations (behold the quotes):
            harp -server app@192.168.59.103:49153 run migration.go "Env1=val migration2.go -arg1 val1"`)
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
				if server == s.String() || server == s.ID {
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
		"DefaultExcludedFiles": [".git/", "tmp/", ".DS_Store", "node_modules/"],
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

func inspectScript(servers []*Server, name string) {
	for _, s := range servers {
		fmt.Println("# ====================================")
		fmt.Println("#", s.String())
		switch name {
		case "deploy":
			fmt.Println(s.retrieveDeployScript())
		case "restart":
			fmt.Println(s.retrieveRestartScript())
		case "kill":
			fmt.Println(s.retrieveKillScript())
		case "rollback":
			fmt.Println(s.retrieveRollbackScript())
		default:
			exitf("unknown script: %s\n", name)
		}
	}
}

func kill(servers []*Server) {
	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(s *Server) {
			defer func() { wg.Done() }()

			session := s.getSession()
			defer session.Close()
			output, err := session.CombinedOutput(s.retrieveKillScript())
			if err != nil {
				exitf("failed to exec %s: %s %s", script, string(output), err)
			}
		}(server)
	}
	wg.Wait()
}

func (s *Server) retrieveKillScript() string {
	s.initPathes()
	var buf bytes.Buffer
	if err := killScriptTmpl.Execute(&buf, struct {
		Config
		*Server
	}{Config: cfg, Server: s}); err != nil {
		exitf(err.Error())
	}
	if debugf {
		fmt.Println(buf.String())
	}
	return buf.String()
}

var killScriptTmpl = template.Must(template.New("").Parse(`set -e
if [[ -f {{.Home}}/harp/{{.App.Name}}/app.pid ]]; then
	target=$(cat {{.Home}}/harp/{{.App.Name}}/app.pid);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
fi`))

func initXC() {
	goroot := strings.TrimSpace(cmd("go", "env", "GOROOT"))
	cmd := exec.Command("./make.bash")
	cmd.Dir = filepath.Join(goroot, "src")
	cmd.Env = append(os.Environ(), "GOOS="+cfg.GOOS, "GOARCH="+cfg.GOARCH)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		exitf("failed to init cross compilation (GOOS=%s, GOARCH=%s): %s", cfg.GOOS, cfg.GOARCH, err)
	}
}

func cleanCaches() {
	defer func() { os.Exit(0) }()
	if keepCache {
		return
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		exitf("os.RemoveAll(%s) error: %s", tmpDir, err)
	}
}
