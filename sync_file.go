package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type fileInfo struct {
	dst, src string
	size     string
}

func (f fileInfo) relDst() string {
	return strings.TrimPrefix(f.dst, filepath.Join(tmpDir, "files")+string(filepath.Separator))
}

var localFiles = map[string]fileInfo{}
var localFilesMux sync.Mutex
var copyFileQueue = make(chan struct{}, 10)

func copyFile(dst, src string) {
	copyFileQueue <- struct{}{}
	defer func() { <-copyFileQueue }()

	srcf, err := os.Open(src)
	if err != nil {
		exitf("os.Open(%s) error: %s", src, err)
	}
	stat, err := srcf.Stat()
	if err != nil {
		exitf("srcf.Stat(%s) error: %s", src, err)
	}

	fi := fileInfo{
		dst:  dst,
		src:  src,
		size: fmtFileSize(stat.Size()),
	}
	localFilesMux.Lock()
	localFiles[dst] = fi
	localFilesMux.Unlock()

	if debugf {
		log.Println(src, stat.Mode())
	}
	if stat.Size() > cfg.App.FileWarningSize {
		fmt.Printf("big file: (%s) %s\n", fi.size, src)
	}
	dstf, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, stat.Mode())
	if err != nil {
		exitf("os.Create(%s) error: %s", dst, err)
	}
	_, err = io.Copy(dstf, srcf)
	if err != nil {
		exitf("io.Copy(%s, %s) error: %s", dst, src, err)
	}
}

func fmtFileSize(size int64) string {
	switch {
	case size > (1 << 60):
		return fmt.Sprintf("%.2f EB", float64(size)/float64(1<<60))
	case size > (1 << 50):
		return fmt.Sprintf("%.2f PB", float64(size)/float64(1<<50))
	case size > (1 << 40):
		return fmt.Sprintf("%.2f TB", float64(size)/float64(1<<40))
	case size > (1 << 30):
		return fmt.Sprintf("%.2f GB", float64(size)/float64(1<<30))
	case size > (1 << 20):
		return fmt.Sprintf("%.2f MB", float64(size)/float64(1<<20))
	case size > (1 << 10):
		return fmt.Sprintf("%.2f KB", float64(size)/float64(1<<10))
	}

	return fmt.Sprint(size)
}
