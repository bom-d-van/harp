#!/usr/bin/env bash

set -e

go build -o tmp/harp

server1=harp_test_server1
server2=harp_test_server2

[ `docker ps -a | grep -o $server1` ] && {
	docker rm -f $server1
}

[ `docker ps -a | grep -o $server2` ] && {
	docker rm -f $server2
}

docker run -P -p 49153:22 -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name $server1 sshd
docker run -P -p 49154:22 -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name $server2 sshd

tmp/harp -c test/harp.json -s prod deploy
tmp/harp -c test/harp.json -s prod migrate "AppEnv=prod test/migration.go -arg1 val1 -arg2 val2" test/migration2.go
