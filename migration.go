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

func migrate(s Server, migrations []string) {
	cmd("mkdir", "-p", "tmp/migrations")

	if !noBuild {
		println("building")
		for _, migration := range migrations {
			base := filepath.Base(migration)
			cmd("go", "build", "-o", "tmp/migrations/"+base, migration)
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

func bundleMigration(migrations []string) {
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
		base := filepath.Base(migration)
		file, err := os.Open("tmp/migrations/" + base)
		if err != nil {
			exitf("failed to open tmp/migrations/%s: %s", base, err)
		}
		fi, err := file.Stat()
		if err != nil {
			exitf("failed to stat %s: %s", file.Name(), err)
		}
		writeToTar(tarw, "migration/"+base, file, fi)
	}
}

func (s Server) uploadMigration(migrations []string) {
	src, err := os.OpenFile("tmp/migrations.tar.gz", os.O_RDONLY, 0644)
	if err != nil {
		exitf("failed to open tmp/migrations.tar.gz: %s", err)
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

		_, err = fmt.Fprintln(dst, "C0644", fi.Size(), "tmp/migrations.tar.gz")
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

	if output, err := session.CombinedOutput("/usr/bin/scp -qrt ./"); err != nil {
		exitf("Failed to run: %s %s", string(output), err)
	}
}

var migrationScript = template.Must(template.New("").Parse(`set -e
tar mxf tmp/migrations.tar.gz
cd {{.Path}}
{{range .Migrations}}
echo running {{.}}
$HOME/migration/{{.}}
{{end}}
`))

func (s Server) runMigration(migrations []string) {
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
	var bases []string
	for _, migration := range migrations {
		bases = append(bases, filepath.Base(migration))
	}
	migrationScript.Execute(&script, struct {
		Migrations []string
		Path       string
	}{Migrations: bases, Path: s.GoPath + "/src/" + cfg.App.ImportPath})

	fmt.Printf("--> %+v\n", script.String())

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

	err := session.Run(script.String())
	if err != nil {
		exitf("failed at runing script: %s\n%s", err, script)
	}
}
