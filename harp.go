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
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// TODOs
// 	rollback
// 	snapshot

type Config struct {
	GOOS, GOARCH              string
	Pkgs                      map[string]string
	Daemons                   []string
	Output                    string
	ServerGoPath, LocalGoPath string
	Files                     []Files
	App                       string
	Log                       string
	Servers                   map[string][]struct {
		Username string
		Addr     string
		Port     string
	}
}

type Files struct {
	// Absolute    bool
	InBin       bool
	Source      string
	Destination string
}

var (
	verbose  bool
	noBuild  bool
	noUpload bool
	noDeploy bool
	tailLog  bool
	script   string

	cfg Config
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "c", "harp.json", "config file path")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.BoolVar(&noBuild, "nb", false, "no build")
	flag.BoolVar(&noUpload, "nu", false, "no upload")
	flag.BoolVar(&noDeploy, "nd", false, "no deploy")
	flag.BoolVar(&tailLog, "log", false, "tail log after deploy")
	flag.StringVar(&script, "scripts", "", "scripts to build and run on server")
	flag.Parse()

	// var scripts = strings.Split(script, ",")

	cfg = parseCfg(configPath)
	if cfg.LocalGoPath == "" {
		cfg.LocalGoPath = os.Getenv("GOPATH")
	}

	info := getInfo()
	println(info)
	return

	sshc, sftpc := getClients()
	defer func() {
		sshc.Close()
		sftpc.Close()
	}()

	if !noBuild {
		for name, path := range cfg.Pkgs {
			cfg.Files = append(cfg.Files, Files{
				InBin:       true,
				Source:      path + "/tmp/" + name,
				Destination: name,
			})
		}
		println("build")
		build()
	}
	if !noUpload {
		println("bundle")
		bundle(info)
		println("upload")
		upload(sshc)
	}
	if !noDeploy {
		println("deploy")
		deploy(sshc)
	}
}

func parseCfg(configPath string) (cfg Config) {
	cfgFile, err := os.OpenFile(configPath, os.O_RDONLY, 0644)
	if err != nil {
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
	if isUsingGit() {
		info += "Git Checksum: " + cmd("git", "rev-parse", "HEAD")
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
		exitf("faied to run %s %s: %s", name, args, err)
	}

	return string(output)
}

func getClients() (client *ssh.Client, ftpClient *sftp.Client) {
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

	serv := cfg.Servers["dev"][0]
	client, err = ssh.Dial("tcp", serv.Addr+serv.Port, config)
	if err != nil {
		exitf("failed to dial: %s", err)
	}

	ftpClient, err = sftp.NewClient(client)
	if err != nil {
		exitf("failed to open sftp client: %s", err)
	}

	return
}

func build() {
	var wg sync.WaitGroup
	for name, pkg := range cfg.Pkgs {
		wg.Add(1)
		go func(name, pkg string) {
			defer wg.Done()

			var cmd = exec.Command("go", "build", "-o", "tmp/"+name, "-tags", "prod", pkg)
			cmd.Dir = cfg.LocalGoPath + "/src/" + pkg
			cmd.Env = os.Environ()
			cmd.Env = append(cmd.Env, "GOOS="+cfg.GOOS, "GOARCH="+cfg.GOARCH)
			output, err := cmd.CombinedOutput()
			if err != nil {
				exitf("failed to build %s: %s: %s", pkg, err, string(output))
			}
		}(name, pkg)
	}
	wg.Wait()
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

	for _, files := range cfg.Files {
		var path = cfg.LocalGoPath + "/src/" + files.Source
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
				name := strings.TrimPrefix(path, cfg.LocalGoPath+"/src/")
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
			if files.InBin {
				p = "bin/" + files.Destination
			} else {
				p = "src/" + files.Source
			}
			writeToTar(tarw, p, file, fi)
		}
	}

	// writeInfoToTar(tarw, info)
}

func upload(sshc *ssh.Client) {
	if verbose {
		log.Println("upload builds.tar.gz to server")
	}
	// dst, err := ftpClient.OpenFile("/home/app/builds.tar.gz", os.O_TRUNC|os.O_CREATE|os.O_WRONLY)
	// if err != nil {
	// 	exitf("failed lstat /home/app/builds: %s", err)
	// }
	// defer func() { dst.Close() }()

	src, err := os.OpenFile("tmp/builds.tar.gz", os.O_RDONLY, 0644)
	if err != nil {
		exitf("failed to open tmp/builds.tar.gz: %s", err)
	}
	defer func() { src.Close() }()

	fi, err := src.Stat()
	if err != nil {
		exitf("failed to retrieve file info of %s: %s", src.Name(), err)
	}

	session, err := sshc.NewSession()
	if err != nil {
		exitf("failed to create session: %s", err)
	}
	defer session.Close()

	go func() {
		dst, err := session.StdinPipe()
		if err != nil {
			exitf("failed to get StdinPipe: %s", err)
		}
		defer dst.Close()

		bar := pb.New(int(fi.Size())).SetUnits(pb.U_BYTES)
		bar.Start()
		dstw := io.MultiWriter(bar, dst)

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "builds.tar.gz")
		if err != nil {
			exitf("failed to open builds.tar.gz: %s", err)
		}
		_, err = io.Copy(dstw, src)
		if err != nil {
			exitf("failed to upload builds.tar.gz: %s", err)
		}
		_, err = fmt.Fprint(dst, "\x00") // 传输以\x00结束
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

// func writeInfoToTar(tarw *tar.Writer, info string) {
// 	header := new(tar.Header)
// 	header.Name = cfg.App + ".info"
// 	header.Size = len(info)
// 	header.Mode = int64(0644)
// 	header.ModTime = time.Now()

// 	err = tarw.WriteHeader(header)
// 	if err != nil {
// 		exitf("failed to write tar header for %s: %s", name, err)
// 	}

// 	_, err = io.Copy(tarw, file)
// 	if err != nil {
// 		exitf("failed to write %s into tar file: %s", name, err)
// 	}
// }

func deploy(sshc *ssh.Client) {
	var logs []string
	var script = "mkdir -p log\n"
	script = "mkdir -p pid\n"
	for pkgd, path := range cfg.Pkgs {
		var log = fmt.Sprintf("/home/app/log/%s.log", pkgd)
		var pid = fmt.Sprintf("/home/app/pid/%s.pid", pkgd)
		logs = append(logs, log)
		script += fmt.Sprintf(`if [[ -f %[1]s ]]; then
	target=$(cat %[1]s);
	if ps -p $target > /dev/null; then
		kill -KILL $target; > /dev/null 2>&1;
	fi
fi
touch %s
`, pid, log)
		script += fmt.Sprintf("cd %s/src/%s\n", cfg.ServerGoPath, path)
		script += fmt.Sprintf("nohup %s/bin/%s 2>&1 >> %s &\n", cfg.ServerGoPath, pkgd, log)
		script += fmt.Sprintf("echo $! > %s\n", pid)
	}
	// script += "tail -f " + strings.Join(logs, " ")
	fmt.Printf("%s", script)

	session, err := sshc.NewSession()
	if err != nil {
		exitf("failed to create session: %s", err)
	}
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
	err = session.Start(script)
	println(string(output))
	if err != nil {
		exitf("failed to exec %s: %s %s", script, string(output), err)
	}
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
