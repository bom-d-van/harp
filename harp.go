package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/DisposaBoy/JsonConfigReader"
)

// TODOs
// 	***	version control (harp version; go version too?)
// 	***	checkers in PATH
// 	***	tail command
// 	**	git status in Info?
// 	*	tmux support for long migrations?

// PRINCIPLES
// KISS
// BC (Being Convinent: all things in one place)

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
	log.SetOutput(os.Stdout)
	if option.debug {
		log.SetFlags(log.Lshortfile)
	} else {
		log.SetFlags(0)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			cleanCaches()
			os.Exit(0)
		}
	}()
}

type Config struct {
	GOOS, GOARCH string

	NoRollback    bool
	RollbackCount int

	// TODO
	BuildVersionCmd string

	// LogDir string `json:"log_dir"`

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

	NoRelMatch       bool
	DefaultExcludeds []string
	Files            []File

	Args []string
	Envs map[string]string

	BuildCmd  string
	BuildArgs string

	KillSig string

	// Default: 1MB
	FileWarningSize int64

	DeployScript    string
	RestartScript   string
	MigrationScript string

	// TODO
	// Hooks struct{}
}

type Tasks []string

func (t Tasks) String() string { return "" }
func (t *Tasks) Set(s string) error {
	migrations = append(migrations, newMigration(s))
	return nil
}

type FlagStrings []string

func (t FlagStrings) String() string { return "" }
func (t *FlagStrings) Set(s string) error {
	*t = append(*t, s)
	return nil
}

var (
	// option is a global control center, keeping flags in one place.
	option = struct {
		configPath string

		debug bool
		// verbose   bool

		noBuild  bool
		noUpload bool
		noDeploy bool
		noFiles  bool
		script   string

		softExclude bool
		keepCache   bool

		toTailLog        bool
		tailBeginLineNum int

		syncFileLimit int

		// TODO: can specify a single server, instead of the whole server set
		servers    FlagStrings
		serverSets FlagStrings
		help       bool
		version    bool

		buildArgs string

		all bool

		deploy string

		tasks Tasks
		hand  bool // hand flag indicates migration are executed but only deployed on servers

		// cli bool

		// transient flag allow running go program without the presence of harp.json
		transient bool

		hook struct {
			before, after string
		}

		docker bool

		force bool
	}{}

	migrations []Migration

	cfg     Config
	GoPaths = strings.Split(os.Getenv("GOPATH"), ":")
	GoPath  = GoPaths[0]
)

var tmpDir = ".harp"

func main() {
	flag.StringVar(&option.configPath, "c", "harp.json", "config file path")

	flag.BoolVar(&option.debug, "debug", false, "print debug info")

	flag.BoolVar(&option.noBuild, "nb", false, "no build")
	flag.BoolVar(&option.noBuild, "no-build", false, "no build")

	flag.BoolVar(&option.noUpload, "nu", false, "no upload")
	flag.BoolVar(&option.noUpload, "no-upload", false, "no upload")

	flag.BoolVar(&option.noDeploy, "nd", false, "no deploy")
	flag.BoolVar(&option.noDeploy, "no-deploy", false, "no deploy")
	flag.BoolVar(&option.noDeploy, "nr", false, "no run (same as -no-deploy)")
	flag.BoolVar(&option.noDeploy, "no-run", false, "no run (same as -no-deploy)")

	flag.BoolVar(&option.noFiles, "nf", false, "no files")
	flag.BoolVar(&option.noFiles, "no-files", false, "no files")

	flag.BoolVar(&option.toTailLog, "log", false, "tail log after deploy")
	flag.IntVar(&option.tailBeginLineNum, "n", 32, "tail log tail localtion line number (tail -n 32)")

	flag.BoolVar(&option.help, "help", false, "print helps")
	flag.BoolVar(&option.help, "h", false, "print helps")

	flag.BoolVar(&option.version, "v", false, "print version num")
	flag.BoolVar(&option.version, "version", false, "print version num")

	flag.BoolVar(&option.softExclude, "soft-exclude", false, "use strings.Contains to exclude files")
	flag.BoolVar(&option.keepCache, "cache", false, "cache data in .harp")

	flag.StringVar(&option.buildArgs, "build-args", "", "build args speicified for building your programs. (default -a -v)")

	flag.Var(&option.serverSets, "s", "specify server sets to deploy, multiple sets are split by comma")
	flag.Var(&option.serverSets, "server-set", "specify server sets to deploy, multiple sets are split by comma")

	flag.Var(&option.servers, "server", "specify servers to deploy, multiple servers are split by comma")

	flag.BoolVar(&option.all, "all", false, "execute action on all server")

	flag.IntVar(&option.syncFileLimit, "sync-queue-size", 5, "set file syncing queue size.")

	flag.StringVar(&option.deploy, "deploy", "", "deploy app to servers/sets")

	flag.Var(&option.tasks, "run", "run go scripts/packages on remote server.")
	flag.BoolVar(&option.hand, "hand", false, "pirnt out shell scripts could be executed by hand on remote servers")

	flag.StringVar(&cfg.GOOS, "goos", "linux", "GOOS")
	flag.StringVar(&cfg.GOARCH, "goarch", "amd64", "GOARCH")
	flag.BoolVar(&option.transient, "t", false, "run migration in transient app")

	flag.BoolVar(&option.force, "f", false, "force harp to deploy. ingore version checking")

	flag.Parse()

	if option.debug {
		log.SetFlags(log.Lshortfile)
	}

	if option.version {
		printVersion()
		return
	}

	var action string
	args := flag.Args()
	switch {
	case len(migrations) > 0:
		action = "run"
	case len(args) > 0:
		action = args[0]
	case len(args) == 0 || option.help:
		printUsage()
		return
	}

	switch action {
	case "init":
		initHarp()
		return
	case "clean":
		option.keepCache = false
		cleanCaches()
		return
	}

	if option.transient {
		cfg.App.Name = "harp"
	} else {
		cfg = parseCfg(option.configPath)
	}

	var servers []*Server
	if action != "cross-compile" && action != "xc" && !(action == "inspect" && args[1] == "files") {
		servers = retrieveServers()
	}

	switch action {
	case "kill":
		kill(servers)
	case "deploy":
		deploy(servers)
	case "migrate", "run":
		// TODO: could specify to run on all servers
		if len(migrations) == 0 {
			migrations = retrieveMigrations(args[1:])
		}
		if len(migrations) == 0 {
			log.Println("please specify migration file or package import path")
			log.Println("e.g. harp -s prod run file.go my/import/path/to/pkg")
			os.Exit(1)
		}
		migrate(servers, migrations)
	case "info", "status":
		info(servers)
	case "log":
		option.toTailLog = true
	case "restart":
		// option.noBuild = true
		// option.noUpload = true
		// deploy(servers)
		restart(servers)
	case "inspect":
		inspectScript(servers, args[1])
	case "rollback":
		if len(args) == 1 {
			fmt.Println("please specify rollback command or version")
			os.Exit(1)
		}
		if args[1] == "l" || args[1] == "ls" || args[1] == "list" {
			lsRollbackVersions(servers, args[1] == "list")
		} else {
			rollback(servers, strings.TrimSpace(args[1]))
		}
	case "cross-compile", "xc":
		initXC()
	case "console", "shell", "sh":
		startConsole(servers)
	default:
		fmt.Println("unknown command:", args[0])
		os.Exit(1)
	}

	if option.toTailLog {
		// if !option.keepCache {
		// 	if err := os.RemoveAll(tmpDir); err != nil {
		// 		exitf("os.RemoveAll(%s) error: %s", tmpDir, err)
		// 	}
		// }
		tailLog(servers, option.tailBeginLineNum)
	}
}

func initTmpDir() func() {
	if err := os.RemoveAll(tmpDir); err != nil {
		exitf("os.RemoveAll(%s) error: %s", tmpDir, err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		exitf("os.MkdirAll(%s) error: %s", tmpDir, err)
	}
	return cleanCaches
}

func deploy(servers []*Server) {
	defer initTmpDir()()

	info := getBuildLog()
	if !option.noBuild {
		log.Println("building")
		build()
	}

	if !option.noUpload {
		syncFiles()
	}

	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(server *Server) {
			defer wg.Done()

			// check harp version
			if err := server.checkHarpVersion(); err != nil {
				if option.force {
					fmt.Fprintf(os.Stderr, err.Error()+"\n")
				} else {
					exitf(err.Error() + "\n")
				}
			}

			if !option.noUpload {
				diff := server.diffFiles()
				if diff != "" {
					diff = "diff: \n" + diff
				}
				log.Printf("uploading: [%s] %s\n%s", server.Set, server, diff)
				server.upload(info)
			}

			if !option.noDeploy {
				log.Printf("deploying: [%s] %s\n", server.Set, server)
				server.deploy()
			}
		}(server)
	}

	wg.Wait()
}

func (s *Server) checkHarpVersion() error {
	output, err := s.getBuildInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] getBuildInfo(): %s\n", s, err)
		return nil
	}
	matches := regexp.MustCompile(harpVersionPrefix + "([0-9\\.]+)\n").FindStringSubmatch(output)
	if len(matches) != 2 {
		fmt.Fprintf(os.Stderr, "[%s] failed to retrieve harp version\n", s)
		return nil
	}
	old := strings.Split(matches[1], ".")
	cur := strings.Split(getVersion(), ".")
	if cmpver(old[0], cur[0]) > 0 || cmpver(old[1], cur[1]) > 0 || cmpver(old[2], cur[2]) > 0 {
		return fmt.Errorf("server %s is deployed by harp version %s; your harp version is %s, please upgrade harp or skip harp version checking by flag -f", s, matches[1], getVersion())
	}
	return nil
}

func cmpver(v1, v2 string) int {
	i1, _ := strconv.Atoi(v1)
	i2, _ := strconv.Atoi(v2)
	return i1 - i2
}

func info(servers []*Server) {
	var wg sync.WaitGroup
	for _, serv := range servers {
		wg.Add(1)
		go func(serv *Server) {
			defer wg.Done()
			serv.initPathes()
			output, err := serv.getBuildInfo()
			if err != nil {
				exitf("failed to cat %s.info on %s: %s(%s)", cfg.App.Name, serv, err, output)
			}
			fmt.Printf("=====\n%s\n%s", serv.String(), output)
		}(serv)
	}
	wg.Wait()
}

func (s *Server) getBuildInfo() (string, error) {
	session := s.getSession()
	output, err := session.CombinedOutput(fmt.Sprintf(
		"cat %s/src/%s/harp-build.info",
		s.GoPath, cfg.App.ImportPath,
	))
	return string(output), err
}

func parseCfg(configPath string) (cfg Config) {
	var r io.Reader
	r, err := os.OpenFile(configPath, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			exitf("Config %s doesn't exist or is unspecified.\nTo specify with flag -c (e.g. -c harp.json)", configPath)
		}
		exitf("failed to read config: %s", err)
	}
	if err := json.NewDecoder(JsonConfigReader.New(r)).Decode(&cfg); err != nil {
		exitf("failed to parse config: %s", err)
	}

	if cfg.App.KillSig == "" {
		cfg.App.KillSig = "KILL"
	}

	for k, set := range cfg.Servers {
		for _, s := range set {
			s.Set = k
		}
	}

	if cfg.RollbackCount == 0 {
		cfg.RollbackCount = 3
	}

	cfg.App.DefaultExcludeds = append(cfg.App.DefaultExcludeds, ".harp/")

	if cfg.App.FileWarningSize == 0 {
		cfg.App.FileWarningSize = 1 << 20
	}

	return
}

const harpVersionPrefix = "Harp Version: "

func getBuildLog() string {
	var info string
	info += "Go Version: " + cmd("go", "version")
	if cfg.GOOS != "" {
		info += "GOOS: " + cfg.GOOS + "\n"
	}
	if cfg.GOARCH != "" {
		info += "GOARCH: " + cfg.GOARCH + "\n"
	}

	info += harpVersionPrefix + getVersion() + "\n"

	vcs, checksum := retrieveChecksum()
	info += vcs + " Checksum: " + checksum + "\n"

	info += "Composer: " + retrieveAuthor() + "\n"

	info += "Build At: " + time.Now().String()

	return info
}

func retrieveChecksum() (vcs, checksum string) {
	checksum = tryCmd("git", "rev-parse", "HEAD")
	if checksum != "" {
		return "Git", strings.TrimSpace(checksum)
	}

	checksum = tryCmd("hg", "id", "-i")
	if checksum != "" {
		return "Hg", strings.TrimSpace(checksum)
	}

	checksum = tryCmd("bzr", "version-info", "--custom", `--template="{revision_id}\n"`)
	if checksum != "" {
		return "Bzr", strings.TrimSpace(checksum)
	}

	return
}

func retrieveAuthor() string {
	name, err := ioutil.ReadFile(".harp-composer")
	if err == nil && len(name) > 0 {
		return strings.TrimSpace(string(name))
	}

	if author := tryCmd("git", "config", "user.name"); author != "" {
		return strings.TrimSpace(author)
	}

	if author := tryCmd("whoami"); author != "" {
		return strings.TrimSpace(author)
	}

	return "anonymous"
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
		exitf("faied to run: %s %s: %s\n%s\n", name, strings.Join(args, " "), err, output)
	}

	return string(output)
}

func tryCmd(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "GOOS="+cfg.GOOS, "GOARCH="+cfg.GOARCH)

	output, err := cmd.CombinedOutput()
	if err != nil && option.debug {
		log.Printf("faied to run %s %s: %s(%s)\n", name, args, err, string(output))
	}

	return string(output)
}

func build() {
	app := cfg.App

	boutput := filepath.Join(tmpDir, app.Name)
	ba := cfg.App.BuildArgs
	if ba == "" {
		ba = "-a -v"
	}
	if option.buildArgs != "" {
		ba = option.buildArgs
	}
	buildCmd := fmt.Sprintf("go build %s -o %s %s", ba, boutput, app.ImportPath)
	if app.BuildCmd != "" {
		buildCmd = fmt.Sprintf(app.BuildCmd, boutput, app.ImportPath)
	}
	if option.debug {
		println("build cmd:", buildCmd)
	}
	output := cmd("sh", "-c", buildCmd)
	if option.debug {
		print(output)
	}
}

func exitf(format string, args ...interface{}) {
	if !strings.HasSuffix(format, "\n") {
		format += "\n"
	}
	fmt.Fprintf(os.Stderr, format, args...)
	if option.debug {
		debug.PrintStack()
	}
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
    info     Print build info of servers (e.g. harp -s prod info). Alias: status.
    log      Print real time logs of application (e.g. harp -s prod log).
    restart  Restart application (e.g. harp -s prod restart).
    init     Initialize a harp.json file.
    rollback
        ls       List all the current releases. Alias: l, list.
        $version Rollback to $version.
    inspect	Inspect script content and others.
    	deploy
    	restart
    	kill
    	rollback
    	files

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
	serverSets := option.serverSets
	servers := option.servers

	if option.all {
		serverSets = []string{}
		for set, _ := range cfg.Servers {
			serverSets = append(serverSets, set)
		}
	}

	if len(servers) == 0 && len(serverSets) == 0 {
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

		// one-shot servers
		if s := newOneShotServer(server); s != nil {
			targetServers = append(targetServers, newOneShotServer(server))
		} else {
			exitf("wrong url format (eg: name@host:port): %s", server)
		}
	}

	for _, s := range targetServers {
		s.init()
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
	appName := filepath.Base(importpath)
	file.WriteString(fmt.Sprintf(`{
	"goos": "linux",
	"goarch": "amd64",
	"app": {
		"name":       "%s",
		"importpath": "%s",
		"envs": {},
		"DefaultExcludeds": [".git/", "tmp/", ".DS_Store", "node_modules/", "*.swp", "*.go"],
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
}`, appName, importpath, importpath))
}

func inspectScript(servers []*Server, name string) {
	if name == "files" {
		inspectFiles()
		return
	}

	for _, s := range servers {
		fmt.Println("# ====================================")
		fmt.Println("#", s.String())
		switch name {
		case "deploy":
			fmt.Println(s.retrieveDeployScript())
		case "restart":
			fmt.Println(s.retrieveRestartScript(retrieveAuthor()))
		case "kill":
			fmt.Println(s.retrieveKillScript(retrieveAuthor()))
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
			output, err := session.CombinedOutput(s.retrieveKillScript(retrieveAuthor()))
			if err != nil {
				exitf("failed to exec %s: %s %s", option.script, string(output), err)
			}
			log.Printf("%s killed\n", s)
		}(server)
	}
	wg.Wait()
}

var killScriptTmpl = template.Must(template.New("").Parse(`set -e
if [[ -f {{.Home}}/harp/{{.App.Name}}/app.pid ]]; then
	target=$(cat {{.Home}}/harp/{{.App.Name}}/app.pid);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
	{{.GetHarpComposer}}
	echo "[harp] {\"datetime\": \"$(date)\", \"user\": \"$harp_composer\", \"type\": \"kill\"}" | tee -a {{.LogPath}} {{.HistoryLogPath}} >/dev/null
fi`))

func (s *Server) retrieveKillScript(who string) string {
	s.initPathes()
	var buf bytes.Buffer
	if err := killScriptTmpl.Execute(&buf, struct {
		Config
		*Server
		GetHarpComposer string
	}{
		Config:          cfg,
		Server:          s,
		GetHarpComposer: s.GetHarpComposer(who),
	}); err != nil {
		exitf(err.Error())
	}
	if option.debug {
		fmt.Println(buf.String())
	}
	return buf.String()
}

func restart(servers []*Server) {
	var wg sync.WaitGroup
	for _, server := range servers {
		wg.Add(1)
		go func(s *Server) {
			defer func() { wg.Done() }()

			session := s.getSession()
			defer session.Close()
			output, err := session.CombinedOutput(s.retrieveRestartScript(retrieveAuthor()))
			if err != nil {
				exitf("failed to exec %s: %s %s", option.script, string(output), err)
			}
			log.Printf("%s restarted\n", s)
		}(server)
	}
	wg.Wait()
}

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
	if option.keepCache {
		return
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		exitf("os.RemoveAll(%s) error: %s", tmpDir, err)
	}
}

func printVersion() { fmt.Println(getVersion()) }

func getVersion() string { return fmt.Sprintf("0.6.%d", version) }
