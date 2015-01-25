#!/usr/bin/env bash

set -e

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

go build -o tmp/harp
tmp/harp -c test/harp.json -s prod deploy
tmp/harp -c test/harp.json -s prod -m test/migration.go,test/migration2.go migrate
