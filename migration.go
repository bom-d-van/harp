package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/cheggaaa/pb"
)

func migrate(s Server, migrations []Migration) {
	cmd("mkdir", "-p", "tmp/migrations")

	if !noBuild {
		println("building")
		for _, migration := range migrations {
			cmd("go", "build", "-o", "tmp/migrations/"+migration.Base, migration.File)
		}

		println("bundling")
		bundleMigration(migrations)
	}

	if !noUpload {
		println("uploading")
		s.uploadMigration(migrations)
	}

	if !noDeploy {
		println("running")
		s.runMigration(migrations)
	}
}

func bundleMigration(migrations []Migration) {
	dst, err := os.OpenFile("tmp/migrations.tar.gz", os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer dst.Close()
	gzipw := gzip.NewWriter(dst)
	defer gzipw.Close()
	tarw := tar.NewWriter(gzipw)
	defer tarw.Close()

	for _, migration := range migrations {
		file, err := os.Open("tmp/migrations/" + migration.Base)
		if err != nil {
			exitf("failed to open tmp/migrations/%s: %s", migration.Base, err)
		}
		fi, err := file.Stat()
		if err != nil {
			exitf("failed to stat %s: %s", file.Name(), err)
		}
		writeToTar(tarw, "migration/"+migration.Base, file, fi)
	}
}

func (s Server) uploadMigration(migrations []Migration) {
	src, err := os.OpenFile("tmp/migrations.tar.gz", os.O_RDONLY, 0644)
	if err != nil {
		exitf("failed to open tmp/migrations.tar.gz: %s", err)
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
{{range .Migrations}}
echo "running {{.Base}}"
{{.Envs}} $HOME/harp/{{$app}}/migration/{{.Base}} {{.Args}}
{{end}}
`))

func (s Server) runMigration(migrations []Migration) {
	// TODO: to refactor
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

	session := s.getSession()
	var script bytes.Buffer
	err := migrationScript.Execute(&script, struct {
		Migrations []Migration
		Path       string
		App        string
	}{Migrations: migrations, Path: path + "/src/" + cfg.App.ImportPath, App: cfg.App.Name})
	if err != nil {
		exitf("failed to generate migration script: %s", err)
	}

	if debugf {
		println(script.String())
	}

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

	if err := session.Run(script.String()); err != nil {
		exitf("failed at runing script: %s\n%s", err, script)
	}
}

type Migration struct {
	File string
	Base string
	Envs string
	Args string
}

// TODO: support file path containing spaces
func retrieveMigrations(args []string) (ms []Migration) {
	for _, arg := range args {
		var migration Migration
		parts := strings.Split(arg, " ")
		for index, part := range parts {
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
			exitf("can't retrieve migration file (migration file path DOES NOT allow SPACES)")
		}

		migration.Envs = strings.Trim(migration.Envs, " ")
		ms = append(ms, migration)
	}

	return
}

func doesFileExist(file string) bool {
	_, err := os.Stat(file)
	if err == nil {
		return true
	}

	_, err = os.Stat(filepath.Join(GoPath, "src", file))
	return err == nil
}
