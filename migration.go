package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/cheggaaa/pb"
	"golang.org/x/crypto/ssh"
)

func migrate(servers []*Server, migrations []Migration) {
	defer initTmpDir()()
	cmd("mkdir", "-p", tmpDir+"/migrations")

	if !option.noBuild {
		println("building")
		for _, migration := range migrations {
			// cmd("go", "build", "-o", tmpDir+"/migrations/"+migration.Base, migration.File)
			output := filepath.Join(tmpDir, "migrations", migration.Base)
			build := fmt.Sprintf("go build -o %s %s", output, migration.File)

			// Note: Build override doesn't support non-import-path migrations
			if cfg.App.BuildCmd != "" {
				build = fmt.Sprintf(cfg.App.BuildCmd, output, migration.File)
			}

			if option.debug {
				println("build cmd:", build)
			}
			cmd("sh", "-c", build)
		}

		println("bundling")
		bundleMigration(migrations)
	}

	var wg sync.WaitGroup
	wg.Add(len(servers))
	for _, server := range servers {
		go func(server *Server) {
			if !option.noUpload {
				println(server.String(), "uploading")
				server.uploadMigration(migrations)
			}

			if !option.noDeploy {
				println(server.String(), "running")
				server.runMigration(migrations)
			}

			wg.Done()
		}(server)
	}
	wg.Wait()
	time.Sleep(time.Second * 2)
}

func bundleMigration(migrations []Migration) {
	dst, err := os.OpenFile(tmpDir+"/migrations.tar.gz", os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer dst.Close()
	gzipw := gzip.NewWriter(dst)
	defer gzipw.Close()
	tarw := tar.NewWriter(gzipw)
	defer tarw.Close()

	for _, migration := range migrations {
		file, err := os.Open(tmpDir + "/migrations/" + migration.Base)
		if err != nil {
			exitf("failed to open %s/migrations/%s: %s", tmpDir, migration.Base, err)
		}
		fi, err := file.Stat()
		if err != nil {
			exitf("failed to stat %s: %s", file.Name(), err)
		}
		writeToTar(tarw, "migration/"+migration.Base, file, fi)
	}
}

func (s *Server) uploadMigration(migrations []Migration) {
	src, err := os.OpenFile(tmpDir+"/migrations.tar.gz", os.O_RDONLY, 0644)
	if err != nil {
		exitf("failed to open %s/migrations.tar.gz: %s", tmpDir, err)
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

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "migrations.tar.gz")
		if err != nil {
			exitf("failed to open migrations.tar.gz: %s", err)
		}
		_, err = io.Copy(dstw, src)
		if err != nil {
			exitf("failed to upload migrations.tar.gz: %s", err)
		}
		_, err = fmt.Fprint(dst, "\x00")
		if err != nil {
			exitf("failed to close migrations.tar.gz: %s", err)
		}
	}()

	if output, err := session.CombinedOutput("/usr/bin/scp -qrt harp/" + cfg.App.Name); err != nil {
		exitf("Failed to run: %s %s", string(output), err)
	}
}

var migrationScript = template.Must(template.New("").Parse(`set -e
{{$app := .App}}
cd harp/{{$app}}
tar mxf migrations.tar.gz
cd {{.Path}}
{{$gopath := .GoPath}}
{{$home := .Home}}
{{range .Migrations}}
echo "running {{.Base}}"
GOPATH="{{$gopath}}" {{.Envs}} {{$home}}/harp/{{$app}}/migration/{{.Base}} {{.Args}}
{{end}}
`))

// 2>&1 | tee -a {{$home}}/harp/{{$app}}/migration.log

func (s *Server) runMigration(migrations []Migration) {
	var envs string
	for k, v := range s.Envs {
		envs += fmt.Sprintf("%s=%s ", k, v)
	}
	for i := range migrations {
		migrations[i].Envs += " " + envs
	}

	session := s.getSession()
	var script bytes.Buffer
	err := migrationScript.Execute(&script, struct {
		Migrations []Migration
		Path       string
		GoPath     string
		App        string
		Home       string
	}{
		Migrations: migrations,
		Path:       s.GoPath + "/src/" + cfg.App.ImportPath,
		GoPath:     s.GoPath,
		App:        cfg.App.Name,
		Home:       s.Home,
	})
	if err != nil {
		exitf("failed to generate migration script: %s", err)
	}

	if s.Config.App.MigrationScript != "" {
		var customScript bytes.Buffer
		file, err := ioutil.ReadFile(s.Config.App.MigrationScript)
		if err != nil {
			exitf("failed to read file (%s): %s", s.Config.App.MigrationScript, err)
		}
		if err := template.Must(template.New("migration.sh").Parse(string(file))).Execute(&customScript, map[string]interface{}{
			"Server":        s,
			"App":           s.Config.App,
			"DefaultScript": script.String(),
		}); err != nil {
			exitf("failed to generate custom script (%s): %s", s.Config.App.MigrationScript, err)
		}
		script = customScript
	}

	if option.debug || option.hand {
		log.Printf("===============\n%s\n%s", s, trimEmptyLines(script.String()))
		if option.hand {
			return
		}
	}

	logSession(session)

	if err := session.Run(script.String()); err != nil {
		exitf("failed at runing script: %s\n%s", err, script)
	}
}

func trimEmptyLines(text string) string {
	return regexp.MustCompile("\n+").ReplaceAllString(text, "\n")
}

func logSession(session *ssh.Session) {
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
}

type Migration struct {
	File string
	Base string
	Envs string
	Args string
}

func newMigration(arg string) Migration {
	var migration Migration
	parts := strings.Split(arg, " ")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "=") {
			migration.Envs += part + " "
		} else if doesFileExist(part) {
			migration.File = part
			migration.Base = filepath.Base(migration.File)
			if len(parts) > index+1 {
				migration.Args = strings.Join(parts[index+1:], " ")
			}
			break
		} else {
			migration.Envs += part + " "
		}
	}

	if migration.File == "" {
		exitf("can't retrieve migration file\n(migration file path DOES NOT allow SPACES)")
	}

	migration.Envs = strings.TrimSpace(migration.Envs)
	return migration
}

// TODO: support file path containing spaces
func retrieveMigrations(args []string) (ms []Migration) {
	for _, arg := range args {
		ms = append(ms, newMigration(arg))
	}

	return
}

func doesFileExist(file string) bool {
	_, err := os.Stat(file)
	if err == nil {
		return true
	}

	for _, path := range GoPaths {
		_, err = os.Stat(filepath.Join(path, "src", file))
		if err == nil {
			return true
		}
	}
	return false
}
