# harp

A go application deploy tool (or an easy way to start a daemon or to run go programs on remote servers).

Note: even though harp is designed to be a local tool. You can use it in your build server and include harp as part of your build script.

## What harp does

Harp simply builds your application and upload it to your server. It brings you a complete solution for deploying common applications. It syncs, restarts, kills, and deploys your applications.

The best way to learn what harp does and helps is to use it. (In test directory, there are docker files and harp configurations you can play with)

## System requirements

Local: Harps works on Mac OS X, Linux, Windows isn't tested.

Server: Harps works on Linux servers.

Third-party requirements: tar, rsync on both server and local.

### Server access using SSH

Harp is using passwordless login with ssh-agent to access your servers. You can find some help from here:

http://www.linuxproblem.org/art_9.html

You can add your key in ssh-agent by:

```sh
ssh-add ~/.ssh/id_rsa # or other path to your private key
ssh-add -l
```

## Examples

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

# Inspect server build info
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

# Run arbitrary Go program wihtout harp.json
harp -server app@test.com:8022 -t run app.go # with default -goos=linux -goarch=amd64

# Run shell commands or execute scripts with harp console (alias: sh, shell)
harp -s prod console
harp -s prod console < my-shell-script
harp -s prod console <<<(ls)
```

## Common Trouble Shootings

### Too many open files

To fix this problem, you need to increase ulimit:

```
ulimit -n 10240 // or any number that is large enough
```

### The .harp directory

Harp creates a temporary directory called .harp in the current path where it is invoked. It will be removed after harp exits. Under rare circumstances, you can use `harp clean` to remove the directory manually. Also, it's better include `.harp` in `.gitignore` or similar counterpart of your VCS tool.

## Configuration

example:

```js
{
	// comments are supported in harp.json
	"GOOS": "linux",   // for go build
	"GOARCH": "amd64", // for go build
	"App": {
		"Name":       "app",
		"ImportPath": "github.com/bom-d-van/harp/test",

		// will be applied to all servers
		"Envs": {
			"var1": "value"
		},

		// these are included in all file Excludeds
		"DefaultExcludeds": [".git/", "tmp/", ".DS_Store", "node_modules/", "*.go"],
		"Files":      [
			// files here could be a string or an object
			"github.com/bom-d-van/harp/test/files",
			{
				"Path": "github.com/bom-d-van/harp/test/file",
				"Excludeds": ["builds"]
			},
			{
				"Path": "github.com/bom-d-van/harp/test/file2",
				// These option will enable rsync --delete during deploy
				// You can check the effect with `harp inspect deploy`
				// Need to be careful with this option.
				//
				// Because it may cause some wanted results, so it's
				// disabled by default
				"Delete": true
			}
		]
	},
	"Servers": {
		"prod": [{
			"ID":  "pluto", // ID field could be used to specify server with `-server` flag
			"User": "app",
			// server specific env vars
			"Envs": {
				"var2": "value2"
			},
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

## Usages

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

You can change how many releases you want to keep by `RollbackCount` or disable rollback by `NoRollback` in harp file.

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

### Build Args Specification

Harp supports go build tool arguments specification.

```
"App": {
	"Name":       "app",
	"BuildArgs":  "-tags daemon"
}
```

You can also override or ad-hoc specify build args from command line as follows:

```
harp -s prod -build-args '-tags client' deploy
```

Note: currently migration using build args from cli are not supported yet.

### Build Override

Harp allows you to override default build command.

Add `BuildCmd` option in `App` as bellow:

```
"App": {
	"Name":       "app",
	"BuildCmd":   "docker run -t -v $GOPATH:/home/app golang  /bin/sh -c 'cd /home/app/src/github.com/bom-d-van/harp; GOPATH=/home/app /usr/local/go/bin/go build -o /home/app/src/github.com/bom-d-van/harp/%s %s'"
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
	"DeployScript": "path-to-your-script-template",
	"RestartScript": "path-to-your-script-template",
	"MigrationScript": "path-to-your-script-template"
},
```

#### Deploy and Restart Script

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

	NoRelMatch       bool
	DefaultExcludeds []string
	Files            []File

	Args []string
	Envs map[string]string

	BuildCmd  string
	BuildArgs string

	KillSig string

	// Default: 1MB
	FileWarningSize int64

	DeployScript  string
	RestartScript string
}

type Server struct {
	ID string

	Envs   map[string]string
	Home   string
	GoPath string
	LogDir string

	User string
	Host string
	Port string

	Set string // aka, Type

	client *ssh.Client

	Config *Config

	// Proxy is for ssh bastion host/ProxyCommand/middleman
	Proxy *Server
}

func (Server) AppRoot() string
```

A default deploy script is:

```
set -e
{{.SyncFiles}}
{{.SaveRelease}}
{{.RestartServer}}
```

Similarly, restart script could be override too. And its default template is:

```
set -e
{{.RestartServer}}
```

You can inspect your script by evoking command: `harp -s prod inspect deploy` or `harp -s prod inspect restart`.

#### Migration Script

Migration script template data is:

```
map[string]interface{}{
	"Server":        harp.Server,
	"App":           harp.App,
	"DefaultScript": string,
}
```

The default migration template contains only the default script. So for now you can only rewrite migration in this fashion:

```
set -e

# custom scripts

{{.DefaultScript}}

# custom scripts
```

### Server Informations

You can use `harp info` to retrieve build and deploy information about the current running server. For example:

```sh
harp info

===== app@192.168.59.103:49153
Go Version: go version go1.4.2 darwin/amd64
GOOS: linux
GOARCH: amd64
Git Checksum: f8eb715f33c36d8ec018fe116491a01540106fc8
Composer: bom_d_van
Build At: 2015-07-06 21:28:55.359181899 +0800 CST

```

Note: Composer means deployer.

You can specify your composer name by saving your name in a file named `.harp-composer`.

### Scripts saved on servers

Harp saves a few scripts on yoru servers after deploy. It could be found in `$HOME/harp/$APP_Name/`.

These scripts could be used as Monitor integration:

* `kill.sh`: kill the application;
* `restart.sh`: restart the application;
* `rollback.sh`: rollback the application: need to specify version (directory names in `releases` folder).

### Initialize go cross compilation

If you need to initialize cross compilation environment, harp has a simple commend to help you:

```
harp xc
```

Note: before using `harp xc`, you need to enable cross compilation by installing go from source, details could be found here: https://golang.org/doc/install/source

### Console (sh, shell)

Run shell commands or execute scripts with harp console (alias: sh, shell)

```
# start a repl to execute shell commands
harp -s prod console

# run a script
harp -s prod console < my-shell-script

# another example
harp -s prod console <<<(ls)
```
