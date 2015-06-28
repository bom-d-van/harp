# harp

A go application deploy tool (or an easy way to start daemon or run go programs on remote servers).

## What harp does

Harp simply builds your application and upload it to your server. It brings you a complete solution for deploying common applications. It syncs, restarts, kills, and deploys your applications.

The best way to learn what harp does and helps is to use it. (In test directory, there are docker files and harp configurations you can play with)

## System requirements

Local: Harps works on Mac OS X, Linus, Windows isn't tested.
Server: Harps works on Linus servers.

Third-party requierments: tar, rsync on both server and local.

## Usage

```sh
# Init harp.json
harp init

# Configure your servers and apps in harp.json. see section Configuration below.
harp -s dev deploy

# Or
harp -s prod deploy

# Restart server
harp -s prod restart

# Shut down server
harp -s prod kill

# Inspect server info
harp -s prod info

# Rollback release
harp -s prod rollback $version-tag

# Tail server logs
harp -s prod log

# Specify config files
harp -s prod -c config/harp.json deploy

# Upload builds/migrations without executing it immediately
harp -s prod -no-run deploy
# Or shorter
harp -s prod -nr deploy

# Skip builds
harp -s prod -no-build deploy

# Skip file uploads
harp -s prod -no-files deploy

# More flags and usages
harp -h
```

## Configuration

example:

```js
{
	"GOOS": "linux",   // for go build
	"GOARCH": "amd64", // for go build
	"App": {
		"Name":       "app",
		"ImportPath": "github.com/bom-d-van/harp/test",

		// these are included in all file Excludeds
		"DefaultExcludeds": [".git/", "tmp/", ".DS_Store", "node_modules/"],
		"Files":      [
			// files here could be a string or an object
			"github.com/bom-d-van/harp/test/files",
			{
				"Path": "github.com/bom-d-van/harp/test/file",
				"Excludeds": ["builds"]
			}
		]
	},
	"Servers": {
		"prod": [{
			"User": "app",
			"Host": "192.168.59.103",
			"Port": ":49155"
		}, {
			"User": "app",
			"Host": "192.168.59.104",
			"Port": ":49156"
		}],

		"dev": [{
			"User": "app",
			"Host": "192.168.59.102",
			"Port": ":49155"
		}]
	}
}
```

### How to specify server or server sets:

Using the configuration above as example, server set means the key in `Servers` object value, i.e. `prod`, `dev`.
While server is elemnt in server set arrays, you can specify it by `{User}@{Host}{Port}`.

```sh
# deploy prod servers
harp -s prod deploy

# deploy dev servers
harp -s dev deploy

# deploy only one prod server:
harp -server app@192.168.59.102:49155 deploy
```

### Migration / Run a go package/file on remote server

You can specify server or server sets on which your migration need to be executed.

Simple:

```
harp -server app@192.168.59.103:49153 run migration.go
```

With env and arguments:

```
harp -server app@192.168.59.103:49153 run "AppEnv=prod migration2.go -arg1 val1 -arg2 val2"
```

Multiple migrations:

```
harp -server app@192.168.59.103:49153 run migration.go "AppEnv=prod migration2.go -arg1 val1 -arg2 val2"
```

__Note__: Harp saved the current migration files in `$HOME/harp/{{.App.Name}}/migrations.tar.gz`. You can uncompress it and execute the binary manually if you prefer or on special occasions.

### Rollback

By default harp will save three most recent releases in `$HOME/harp/{{.App.Name}}/releases` directory. The current release is the newest release in the releases list.

```
# list all releases
harp -s prod rollback ls

# rollback
harp -s prod rollback 15-06-14-11:29:14
```

And there is also a `rollback.sh` script in `$HOME/harp/{{.App.Name}}` that you can use to rollback release.

You can change how many releases you want to keep by `RollbackCount` or disable rollback by `NoRollback` in configs.

```
{
	"GOOS": "linux",   // for go build
	"GOARCH": "amd64", // for go build

	"NoRollback": true,
	"RollbackCount": 10,

	"App": {
		...
	},
	...
}
```

### Build Override

Add `BuildCmd` option in `App` as bellow:

```
"App": {
	"Name":       "app",
	"BuildCmd":   "docker run -t -v $GOPATH:/home/app golang  /bin/sh -c 'GOPATH=/home/app /usr/local/go/bin/go build -o /home/app/src/github.com/bom-d-van/harp/%s %s'"
}
```

The first `%s` represents the harp temporary output, and the second `%s` represents a import path or a file path for some migrations. An example output in `test/harp2.json` is:

```sh
# for normal builds
docker run -t -v $GOPATH:/home/app golang  /bin/sh -c 'GOPATH=/home/app /usr/local/go/bin/go build -o \$GOPATH/src/github.com/bom-d-van/harp/.harp/app github.com/bom-d-van/harp/test'

# for migrations/runs
docker run -t -v $GOPATH:/home/app golang  /bin/sh -c 'GOPATH=/home/app /usr/local/go/bin/go build -o \$GOPATH/src/github.com/bom-d-van/harp/.harp/migrations/migration3.go github.com/bom-d-van/harp/test/migration3'
```

__NOTE:__ Build override doesn't support non-package migrations. i.e. every migration under build override has to be a legal go package.

Build override is useful doing cross compilation for cgo-involved projects, e.g. using Mac OS X building Linux binaries by docker or any other tools etc.

Note: Harps is saving temporary build output and files in `$(pwd)/.harp`. Therefore harp expects build output appears in directory `$(pwd)/.harp/{{app name}}` where you evoke harp command (i.e. pwd). And `$(pwd)/.harp/migrations/{{migration name}}` for migrations.

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

type App struct {
	Name       string
	ImportPath string
	Files      []string

	Args []string
	Envs map[string]string

	BuildCmd string

	KillSig string

	// TODO: could override default deploy script for out-of-band deploy
	DeployScript  string
	RestartScript string
}

type Server struct {
	Envs   map[string]string
	GoPath string
	LogDir string
	PIDDir string

	User string
	Host string
	Port string

	Set string

	client *ssh.Client
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

### Initialize go cross compilation

If you need to initialize cross compilation environment, harp has a simple commend to help you:

```
harp xc
```
