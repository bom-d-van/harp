#!/usr/bin/env bash

docker run -P -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name server1 sshd
docker run -P -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name server2 sshd

go run harp.go -c test/harp.json