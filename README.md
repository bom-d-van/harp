# harp

a go application deploy tool.

## What harp does

Harp simply builds your application and upload it to your server. It brings you a complete solution for deploying common applications. It syncs, restarts, kills, and deploys your applications.

The best way to learn what harp does and helps to use it. (In test directory, there are docker files and harp configurations you can play with)

## usage

```sh
harp is a go application deployment tool.
usage:
    harp [options] [action]
actions:
    deploy  Deploy your application (e.g. harp -s prod deploy).
    migrate Run migrations on server (e.g. harp -s prod -m migration/reset_info.go migrate).
    info    Print build info of servers (e.g. harp -s prod info).
    log     Print real time logs of application (e.g. harp -s prod log).
    restart Restart application (e.g. harp -s prod restart).
options:
  -c="harp.json": config file path
  -debug=false: print debug info
  -h=false: print helps
  -help=false: print helps
  -m="": specify migrations to run on server, multiple migrations are split by comma
  -nb=false: no build
  -nd=false: no deploy
  -nu=false: no upload
  -s="": specify server sets to deploy, multiple sets are split by comma
  -scripts="": scripts to build and run on server
  -server-set="": specify server sets to deploy, multiple sets are split by comma
  -v=false: verbose
```

## configuration

example:

```json
{
	"GOOS": "linux",   // for go build
	"GOARCH": "amd64", // for go build
	"App": {
		"Name":       "app",
		"ImportPath": "github.com/bom-d-van/harp/test",
		"Files":      [
			"github.com/bom-d-van/harp/test/files",
			"github.com/bom-d-van/harp/test/file"
		]
	},
	"Servers": {
		"prod": [{
			"User": "app",
			"Host": "192.168.59.103",
			"Port": ":49155"
		}, {
			"User": "app",
			"Host": "192.168.59.103",
			"Port": ":49156"
		}]
	}
}
```

### Build Override

Add `BuildCmd` option in `App` as bellow:

```
"App": {
	"Name":       "app",
	"BuildCmd":   "docker run -t -v $GOPATH:/home/app golang  /bin/sh -c 'GOPATH=/home/app /usr/local/go/bin/go build -o path/to/app/tmp/app project/import/path'"
}
```

Build override is useful doing cross compilation for cgo-involved projects, e.g. using Mac OS X building Linux binaries by docker or any other tools etc.

Note: harp expects build output appears in directory `tmp/{{app name}}` where you evoke harp command (i.e. pwd).

### Script Override

harp supports you to override its default deploy script. Add configuration like bellow:

```
"App": {
	"Name":         "app",
	"DeployScript": "path-to-your-script-template"
},
```

The script could be a `text/template.Template`, into which harp pass a data as bellow:

```
map[string]interface{}{
	"App":           harp.App,
	"Server":        harp.Server,
	"SyncFiles":     syncFilesScript,
	"RestartServer": restartScript,
}
```

A default deploy script is:

```
set -e
{{.SyncFiles}}
{{.RestartServer}}
```

Similarly, restart script could be override too. And its default template is:

```
set -e
{{.RestartServer}}
```

You can inspect your script by evoking command: `harp -s prod inspect deploy` or `harp -s prod inspect restart`.