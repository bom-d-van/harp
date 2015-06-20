package main

import (
	"strconv"

	"encoding/json"
)

type File struct {
	Path     string
	Excludes []string
}

// "rsync", "-P", "-az", "--delete", "-e", ssh, "tmp/"+appName, dst

func (f *File) UnmarshalJSON(data []byte) (err error) {
	if !(len(data) > 2 && data[0] == '{' && data[len(data)-1] == '}') {
		f.Path, err = strconv.Unquote(string(data))
		return
	}

	var v struct {
		Path     string
		Excludes []string
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return
	}

	f.Path = v.Path
	f.Excludes = v.Excludes
	return
}
