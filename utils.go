package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

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
	header.Name = fmt.Sprintf("src/%s/harp-build.info", cfg.App.ImportPath)
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

func runCmd(sshc *ssh.Client, cmd string) (output []byte) {
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
