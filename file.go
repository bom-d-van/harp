package main

import (
	"strconv"

	"encoding/json"
)

type File struct {
	file
}

type file struct {
	Path      string
	Excludeds []string
	Delete    bool // delete extraneous files from dest dirs
}

// "rsync", "-P", "-az", "--delete", "-e", ssh, "tmp/"+appName, dst

func (f *File) UnmarshalJSON(data []byte) (err error) {
	if !(len(data) > 2 && data[0] == '{' && data[len(data)-1] == '}') {
		f.Path, err = strconv.Unquote(string(data))
		return
	}

	if err = json.Unmarshal(data, &f.file); err != nil {
		return
	}

	return
}
