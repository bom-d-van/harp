# harp

a go application deploy tool.

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
	"GOOS": "linux",
	"GOARCH": "amd64",
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
