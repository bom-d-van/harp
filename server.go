package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Server struct {
	ID string

	Envs map[string]string
	// Args   []string // TODO: support

	Home   string
	GoPath string
	LogDir string
	// PIDDir string

	User string
	Host string
	Port string

	// TODO
	Password string

	Set string // aka, Type

	client *ssh.Client

	Config *Config

	Proxy *Server
}

var urlRegexp = regexp.MustCompile(`(?P<user>[^@]+)@(?P<host>[^:]+)(?P<port>:.*)?`)
var testMode bool

func newOneShotServer(url string) *Server {
	if !urlRegexp.MatchString(url) {
		return nil
	}
	matches := urlRegexp.FindStringSubmatch(url)
	var s Server
	s.User = matches[1]
	s.Host = matches[2]
	s.Port = matches[3]
	if s.Port == "" {
		s.Port = ":22"
	}

	if !testMode {
		s.init()
	}

	return &s
}

func (s *Server) init() {
	s.Config = &cfg
	if s.User == "" {
		fmt.Printf("%s contains server with empty user name\n", s.Set)
		os.Exit(1)
	} else if s.Host == "" {
		fmt.Printf("%s contains server with empty host\n", s.Set)
		os.Exit(1)
	}
	if s.Port == "" {
		s.Port = ":22"
	}

	s.initSetUp()
	s.initPathes()
}

// TODO: pipelining output instead of be silently totally
// copy files into tmp/harp/
// exclude files
func (s *Server) upload(info string) {
	// rsync -av -e 'ssh -o "ProxyCommand ssh -p port bastion-dev@proxy exec nc %h %p 2>/dev/null"' test.txt app@target:~/
	// rsync -avrP -e 'ssh -o ProxyCommand="ssh -W %h:%p bastion-dev@proxy -p port"' test.txt app@target:~/
	ssh := fmt.Sprintf(`ssh -l %s -p %s`, s.User, strings.TrimLeft(s.Port, ":"))
	if s.Proxy != nil {
		ssh = fmt.Sprintf(`ssh -o ProxyCommand="ssh -W %%h:%%p %s@%s -p %s"`, s.Proxy.User, s.Proxy.Host, strings.TrimLeft(s.Proxy.Port, ":"))
	}

	appName := cfg.App.Name
	dst := fmt.Sprintf("%s@%s:%s/harp/%s/", s.User, s.Host, s.Home, appName)
	// if option.debug {
	// 	fmt.Println("rsync", "-az", "--delete", "-e", ssh, filepath.Join(tmpDir, appName), filepath.Join(tmpDir, "files"), dst)
	// }
	args := []string{"-az", "--delete", "-e", ssh}
	if option.debug {
		args = append(args, "-P")
	}
	if !option.noBuild {
		args = append(args, filepath.Join(tmpDir, appName))
	}
	if !option.noFiles {
		args = append(args, filepath.Join(tmpDir, "files"))
	}
	if option.debug {
		fmt.Println("upload cmd:", strings.Join(append([]string{"rsync"}, append(args, dst)...), " "))
	}
	cmd := exec.Command("rsync", append(args, dst)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		exitf("failed to sync binary %s: %s", appName, err)
	}

	session := s.getSession()
	output, err := session.CombinedOutput(fmt.Sprintf("cat <<EOF > %s/harp/%s/harp-build.info\n%s\nEOF", s.Home, appName, info))
	if err != nil {
		exitf("failed to save build info: %s: %s", err, string(output))
	}
	session.Close()
}

func (s *Server) deploy() {
	// if option.debug {
	// 	log.Println("deplying", s.String())
	// }

	// TODO: save scripts(s) for kill app
	s.saveScript("restart", s.retrieveRestartScript())
	s.saveScript("kill", s.retrieveKillScript())
	s.saveScript("rollback", s.retrieveRollbackScript())

	// var output []byte
	session := s.getSession()
	defer session.Close()

	script := s.retrieveDeployScript()
	if option.debug {
		fmt.Printf("%s", script)
	}
	if output, err := session.CombinedOutput(script); err != nil {
		exitf("failed to exec %s: %s %s", script, string(output), err)
	}

	// clean older releases
	if !cfg.NoRollback {
		s.trimOldReleases()
	}
}

func (s *Server) scriptData() interface{} {
	return map[string]interface{}{
		"App":           cfg.App,
		"Server":        s,
		"SyncFiles":     s.syncFilesScript(),
		"RestartServer": s.restartScript(),
		"SaveRelease":   s.saveReleaseScript(),
	}
}

func (s *Server) syncFilesScript() (script string) {
	script += fmt.Sprintf("mkdir -p %s/bin %s/src %s/src/%s\n", s.GoPath, s.GoPath, s.GoPath, cfg.App.ImportPath)

	// TODO: handle callback error
	for _, dstf := range cfg.App.Files {
		dst := dstf.Path
		src := fmt.Sprintf("%s/harp/%s/files/%s", s.Home, cfg.App.Name, strings.Replace(dst, "/", "_", -1))
		odst := dst
		dst = fmt.Sprintf("%s/src/%s", s.GoPath, dst)

		var hasErr bool
		for _, path := range GoPaths {
			hasErr = false
			if fi, err := os.Stat(filepath.Join(path, "src", odst)); err != nil {
				hasErr = true
			} else if fi.IsDir() {
				src += "/"
				dst += "/"
			}
		}
		if hasErr {
			exitf("failed to find %s from %s", odst, GoPaths)
		}

		script += fmt.Sprintf("mkdir -p \"%s\"\n", filepath.Dir(dst))
		var delete string
		if dstf.Delete {
			delete = "--delete"
		}
		var excludes []string
		for _, e := range dstf.Excludeds {
			excludes = append(excludes, fmt.Sprintf("--exclude '%s'", e))
		}
		script += fmt.Sprintf("rsync -az %s %s \"%s\" \"%s\"\n", delete, strings.Join(excludes, " "), src, dst)
	}

	script += fmt.Sprintf("cp %s/harp/%s/harp-build.info %s/src/%s/\n", s.Home, cfg.App.Name, s.GoPath, cfg.App.ImportPath)
	// rsync += fmt.Sprintf("rsync -az --delete harp/%[1]s/%[1]s %s/bin/%[1]s\n", cfg.App.Name, s.GoPath)
	script += fmt.Sprintf("rsync -az %s/harp/%[2]s/%[2]s %[3]s/bin/%[2]s\n", s.Home, cfg.App.Name, s.GoPath)

	if script[len(script)-1] == '\n' {
		script = script[:len(script)-1]
	}
	return
}

func (s *Server) GetLogDir() string {
	dir := s.LogDir
	if dir == "" {
		dir = fmt.Sprintf("%s/harp/%s/log", s.Home, cfg.App.Name)
	}
	return dir
}

func (s *Server) LogPath() string {
	return filepath.Join(s.GetLogDir(), "app.log")
}

func (s *Server) PIDPath() string {
	return fmt.Sprintf("%s/harp/%s/app.pid", s.Home, cfg.App.Name)
}

var restartScriptTmpl = template.Must(template.New("").Parse(`if [[ -f {{.PIDPath}} ]]; then
	target=$(cat {{.PIDPath}});
	if ps -p $target > /dev/null; then
		kill -{{.Config.App.KillSig}} $target; > /dev/null 2>&1;
	fi
fi
mkdir -p {{.GetLogDir}}
touch {{.LogPath}}
`))

func (s *Server) restartScript() (script string) {
	app := cfg.App
	log := s.LogPath()
	pid := s.PIDPath()
	// 	script += fmt.Sprintf(`if [[ -f %[1]s ]]; then
	// 	target=$(cat %[1]s);
	// 	if ps -p $target > /dev/null; then
	// 		kill -%[4]s $target; > /dev/null 2>&1;
	// 	fi
	// fi
	// touch %[2]s
	// `, pid, log, app.Name, app.KillSig)
	var buf bytes.Buffer
	if err := restartScriptTmpl.Execute(&buf, s); err != nil {
		exitf("failed to execute restartScriptTmpl: %s", err)
	}
	script += buf.String()

	envs := fmt.Sprintf(`%s=%q`, "GOPATH", s.GoPath)
	for k, v := range app.Envs {
		envs += fmt.Sprintf(` %s="%s"`, k, v)
	}
	for k, v := range s.Envs {
		envs += fmt.Sprintf(` %s="%s"`, k, v)
	}
	args := strings.Join(app.Args, " ")
	script += fmt.Sprintf("cd %s/src/%s\n", s.GoPath, app.ImportPath)
	// env=val nohup $GOPATH/bin/$app arg1 >> $log 2&1 &
	script += fmt.Sprintf(`echo "[harp] $(date) server deployed/restarted" >> %s`+"\n", log)
	script += fmt.Sprintf("%s nohup %s/bin/%s %s >> %s 2>&1 &\n", envs, s.GoPath, app.Name, args, log)
	script += fmt.Sprintf("echo $! > %s\n", pid)
	script += "cd " + s.Home
	return
}

var releaseTsOnce sync.Once
var releaseTs string

func (s *Server) saveReleaseScript() (script string) {
	if cfg.NoRollback {
		return
	}

	releaseTsOnce.Do(func() { releaseTs = time.Now().Format("06-01-02-15:04:05") })

	script += fmt.Sprintf(`cd %s/harp/%s
if [[ -f harp-build.info ]]; then
	mkdir -p releases/%s
	cp -rf %s harp-build.info files kill.sh restart.sh rollback.sh releases/%s
fi`, s.Home, cfg.App.Name, releaseTs, cfg.App.Name, releaseTs)
	return
}

func (s *Server) retrieveDeployScript() string {
	script := defaultDeployScript
	if cfg.App.DeployScript != "" {
		cont, err := ioutil.ReadFile(cfg.App.DeployScript)
		if err != nil {
			exitf(err.Error())
		}
		script = string(cont)
	}
	tmpl, err := template.New("").Parse(script)
	if err != nil {
		exitf(err.Error())
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, s.scriptData()); err != nil {
		exitf(err.Error())
	}

	return buf.String()
}

const defaultDeployScript = `set -e
{{.SyncFiles}}
{{.SaveRelease}}
{{.RestartServer}}
`

func (s *Server) saveScript(name, script string) {
	session := s.getSession()
	defer session.Close()
	cmd := fmt.Sprintf(`cat <<EOF > %s/harp/%s/%s.sh
%s
EOF
chmod +x %s/harp/%s/%s.sh
`, s.Home, cfg.App.Name, name, script, s.Home, cfg.App.Name, name)
	cmd = strings.Replace(cmd, "$", "\\$", -1)
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		exitf("failed to save kill script on %s: %s: %s", s, err, string(output))
	}
}

func (s *Server) retrieveRollbackScript() string {
	data := struct {
		Config
		*Server
		SyncFiles     string
		RestartScript string
	}{
		Config:        cfg,
		Server:        s,
		SyncFiles:     s.syncFilesScript(),
		RestartScript: s.restartScript(),
	}
	var buf bytes.Buffer
	if err := rollbackScriptTmpl.Execute(&buf, data); err != nil {
		exitf(err.Error())
	}
	if option.debug {
		fmt.Println(buf.String())
	}
	return buf.String()
}

var rollbackScriptTmpl = template.Must(template.New("").Parse(`set -e
version=$1
if [[ $version == "" ]]; then
	echo "please specify version in the following list to rollback:"
	ls -1 {{.Home}}/harp/{{.App.Name}}/releases
	exit 1
fi

for file in $(ls {{.Home}}/harp/{{.App.Name}}/releases/$version); do
	rm -rf {{.Home}}/harp/{{.App.Name}}/$file
	cp -rf {{.Home}}/harp/{{.App.Name}}/releases/$version/$file {{.Home}}/harp/{{.App.Name}}/$file
done

{{.SyncFiles}}

{{.RestartScript}}`))

func (s Server) retrieveRestartScript() string {
	script := defaultRestartScript
	if cfg.App.RestartScript != "" {
		cont, err := ioutil.ReadFile(cfg.App.RestartScript)
		if err != nil {
			exitf(err.Error())
		}
		script = string(cont)
	}
	tmpl, err := template.New("").Parse(script)
	if err != nil {
		exitf(err.Error())
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, s.scriptData()); err != nil {
		exitf(err.Error())
	}

	return buf.String()
}

const defaultRestartScript = `set -e
{{.RestartServer}}
`

func (s *Server) initPathes() {
	if s.Home == "" {
		session := s.getSession()
		output, err := session.CombinedOutput("echo $HOME")
		if err != nil {
			fmt.Printf("echo $HOME on %s error: %s\n", s, err)
		}
		session.Close()
		s.Home = strings.TrimSpace(string(output))
	}
	if s.Home == "" {
		session := s.getSession()
		output, err := session.CombinedOutput("pwd")
		if err != nil {
			fmt.Printf("pwd on %s error: %s\n", s, err)
		}
		session.Close()
		s.Home = strings.TrimSpace(string(output))
	}

	if s.GoPath == "" {
		session := s.getSession()
		output, err := session.CombinedOutput("echo $GOPATH")
		if err != nil {
			fmt.Printf("echo $GOPATH on %s error: %s\n", s, err)
		}
		session.Close()
		s.GoPath = strings.TrimSpace(string(output))
	}
	if s.GoPath == "" {
		s.GoPath = s.Home
	}
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

func (s *Server) exec(cmd string) string {
	if s.client == nil {
		s.initClient()
	}

	session, err := s.client.NewSession()
	if err != nil {
		// fmt.Printf("%s: %s\n", s, err)
		return err.Error()
	}

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		output = append([]byte(err.Error()+"\n"), output...)
	}
	session.Close()
	return string(output)
}

// name@host:port
func (s Server) String() string {
	return fmt.Sprintf("%s@%s%s", s.User, s.Host, s.Port)
}

// TODO: add tests
func (s *Server) initClient() {
	user := s.User
	if s.Proxy != nil {
		user = s.Proxy.User
	}

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
		User: user,
		Auth: auths,
	}

	dst := s.Host + s.Port
	if s.Proxy != nil {
		dst = s.Proxy.Host + s.Proxy.Port
	}
	s.client, err = ssh.Dial("tcp", dst, config)
	if err != nil {
		serv := s
		if s.Proxy != nil {
			serv = s.Proxy
		}
		fmt.Fprintf(os.Stderr, "failed to dial %s: %s\n\n", serv, err)
		fmt.Println("Harp is using ssh-agent and passwordless-login to access your servers.")
		fmt.Println("Make sure you have added your private key in ssh-agent (ssh-add -l).")
		fmt.Println("More information could be found here: https://github.com/bom-d-van/harp#server-access-using-ssh")

		if option.debug {
			debug.PrintStack()
		}
		os.Exit(1)
	}

	if s.Proxy == nil {
		return
	}

	bastionConn, err := s.client.Dial("tcp", s.Host+s.Port)
	if err != nil {
		exitf("failed to dial %s from bastion host %s: %s", s, s.Proxy, err)
	}

	conn, newChan, reqs, err := ssh.NewClientConn(bastionConn, s.Host+s.Port, &ssh.ClientConfig{
		User: s.User,
		Auth: auths,
	})
	if err != nil {
		exitf("Failed to handshake server %s from server %s: %s", s, s.Proxy, err)
	}
	s.client = ssh.NewClient(conn, newChan, reqs)
}

func (s *Server) initSetUp() {
	if s.client == nil {
		s.initClient()
	}
	runCmd(s.client, fmt.Sprintf("mkdir -p harp/%s/files", cfg.App.Name))
}

// TODO: add test
func (s *Server) diffFiles() string {
	session := s.getSession()
	fileRoot := fmt.Sprintf("%s/harp/%s/files/", s.Home, cfg.App.Name)
	cmd := fmt.Sprintf(`if [[ -d "%s/harp/%s/" ]]; then
		find %s -type f
	fi`, s.Home, cfg.App.Name, fileRoot)
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		exitf("failed to retrieve files: %s: %s %s", cmd, err, output)
	}
	session.Close()
	serverFiles := map[string]struct{}{}
	for _, f := range strings.Split(string(output), "\n") {
		if f == "" {
			continue
		}
		serverFiles[strings.TrimPrefix(f, fileRoot)] = struct{}{}
	}

	var diff string
	for _, f := range localFiles {
		if _, ok := serverFiles[f.relDst()]; !ok {
			diff += fmt.Sprintf("+ %s %s\n", f, f.src)
		}
	}

	for sfile := range serverFiles {
		if _, ok := localFiles[filepath.Join(tmpDir, "files", sfile)]; !ok {
			diff += fmt.Sprintf("- %s\n", sfile)
		}
	}

	return diff
}

func (s *Server) prompt() string {
	whoami := strings.TrimSpace(s.exec("whoami"))
	hostname := strings.TrimSpace(s.exec("hostname"))
	if whoami == "" || hostname == "" {
		return fmt.Sprintf("%s:%s$", s, s.Home)
	}
	return fmt.Sprintf("%s@%s:%s$", whoami, hostname, s.Home)
}

func (s *Server) AppRoot() string {
	return fmt.Sprintf("%s/src/%s/", s.GoPath, cfg.App.ImportPath)
}
