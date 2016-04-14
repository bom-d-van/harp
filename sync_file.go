package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

func syncFiles() {
	if !copyFileNop {
		log.Println("syncing files")
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "files"), 0755); err != nil {
		exitf("os.MkdirAll(.harp/files) error: %s", err)
	}

	var wg sync.WaitGroup
	for _, f := range cfg.App.Files {
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
			if os.IsNotExist(err) {
				exitf("%s doesn't exist", src)
			}
			exitf("os.Stat(%s) error: %s", src, err)
		} else if fi.IsDir() {
			if option.debug {
				log.Println(dst, fi.Mode())
			}
			if !copyFileNop {
				if err := os.Mkdir(dst, fi.Mode()); err != nil {
					exitf("os.Mkdir(%s) error: %s", dst, err)
				}
			}
		} else {
			// a single file speicified in Files.
			copyFile(dst, src)
			continue
		}

		// handle directory here
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

			for _, exu := range append(cfg.App.DefaultExcludeds, f.Excludeds...) {
				matched, err := filepath.Match(exu, rel)
				if err != nil {
					exitf("filepath.Match(%s, %s) error: %s", exu, rel, err)
				}
				if !matched && !option.softExclude {
					matched = strings.Contains(rel, exu)
				}
				// TODO: add test
				if !matched && !cfg.App.NoRelMatch && !info.IsDir() {
					matched, err = filepath.Match(exu, filepath.Base(rel))
					if err != nil {
						exitf("filepath.Match(%s, filepath.Base(%s)) error: %s", exu, rel, err)
					}
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
				if option.debug {
					log.Println(filepath.Join(dst, rel), info.Mode())
				}
				if !copyFileNop {
					if err := os.Mkdir(filepath.Join(dst, rel), info.Mode()); err != nil {
						exitf("os.Mkdir(%s) error: %s", filepath.Join(dst, rel), err)
					}
				}
				return nil
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				copyFile(filepath.Join(dst, rel), path)
			}()
			return nil
		})
		if err != nil && err != filepath.SkipDir {
			exitf("walking %s: %s", src, err)
		}
	}
	wg.Wait()
}

type fileInfo struct {
	dst, src string
	size     int64
}

func (f fileInfo) relDst() string {
	return strings.TrimPrefix(f.dst, filepath.Join(tmpDir, "files")+string(filepath.Separator))
}

var localFiles = map[string]fileInfo{}
var localFilesMux sync.Mutex
var copyFileQueue = make(chan struct{}, runtime.NumCPU())
var copyFileNop bool

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
	defer srcf.Close()

	fi := fileInfo{
		dst:  dst,
		src:  src,
		size: stat.Size(),
	}
	localFilesMux.Lock()
	localFiles[dst] = fi
	localFilesMux.Unlock()

	if option.debug {
		log.Println(src, stat.Mode())
	}
	if stat.Size() > cfg.App.FileWarningSize {
		fmt.Printf("big file: (%s) %s\n", fi, src)
	}

	if copyFileNop {
		return
	}

	dstf, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, stat.Mode())
	if err != nil {
		exitf("os.Create(%s) error: %s", dst, err)
	}
	_, err = io.Copy(dstf, srcf)
	if err != nil {
		exitf("io.Copy(%s, %s) error: %s", dst, src, err)
	}
	defer dstf.Close()
}

func (f fileInfo) String() string { return fmtFileSize(f.size) }

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

func inspectFiles() {
	copyFileNop = true
	syncFiles()
	var size int64
	files := make([]string, 0, len(localFiles))
	for _, f := range localFiles {
		size += f.size
		files = append(files, f.src)
	}
	sort.Strings(files)
	for _, f := range files {
		log.Println(f)
	}
	log.Printf("count: %d\nsize: %s\n", len(files), fmtFileSize(size))
}
