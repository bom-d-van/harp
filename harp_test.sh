#!/usr/bin/env bash

# TODO: retire this script with go tests

set -e

go build -o tmp/harp

server1=harp_test_server1
server2=harp_test_server2
dmip=`docker-machine ip default`

[ `docker ps -a | grep -o $server1` ] && {
	docker rm -f $server1
}

[ `docker ps -a | grep -o $server2` ] && {
	docker rm -f $server2
}

docker run -P -p 49153:22 -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name $server1 sshd
docker run -P -p 49154:22 -d -v ~/.ssh/id_rsa.pub:/home/app/.ssh/authorized_keys --name $server2 sshd

# trap 'cat tmp/test.log' ERR
# rm -f tmp/test.log

# create a big file
dd if=/dev/zero of=test/files/big-file bs=$((3<<20)) count=1
touch test/files/test.swp

echo ====================
echo tmp/harp -c test/harp.json -s prod deploy
test/update_pkg.sh
tmp/harp -c test/harp.json -s prod deploy

echo ====================
echo tmp/harp -c test/harp2.json -s prod deploy
test/update_pkg.sh
tmp/harp -c test/harp2.json -s prod deploy
ssh app@$dmip -p 49153 -- cat test.log

echo ====================
echo tmp/harp -c test/harp3.json -s prod deploy
test/update_pkg.sh
tmp/harp -c test/harp3.json -s prod deploy
ssh app@$dmip -p 49153 -- cat harp/app/log/app.log

echo tmp/harp -c test/harp.json -build-args "-tags argtag" -server app@$dmip:49153 deploy
tmp/harp -c test/harp.json -build-args "-tags argtag" -server app@$dmip:49153 deploy

echo ====================
echo tmp/harp -c test/harp.json -server app@$dmip:49153 deploy
tmp/harp -c test/harp.json -server app@$dmip:49153 deploy

echo ====================
echo tmp/harp -c test/harp.json -server app@$dmip:49153 -run "AppEnv=prod test/migration.go -arg1 val1 -arg2 val2" -run test/migration2.go
tmp/harp -c test/harp.json -server app@$dmip:49153 -run "AppEnv=prod test/migration.go -arg1 val1 -arg2 val2" -run test/migration2.go

echo ====================
echo tmp/harp -c test/harp.json -s prod -run github.com/bom-d-van/harp/test/migration3
tmp/harp -c test/harp.json -s prod -run github.com/bom-d-van/harp/test/migration3

echo ====================
echo tmp/harp -c test/harp2.json -s prod -run github.com/bom-d-van/harp/test/migration3
tmp/harp -c test/harp2.json -s prod -run github.com/bom-d-van/harp/test/migration3

echo ====================
echo tmp/harp -c test/harp.json -s prod rollback ls
for version in `tmp/harp -c test/harp.json -s prod rollback ls | tail -2`; do
	echo "tmp/harp -c test/harp.json -s prod rollback $version"
	tmp/harp -c test/harp.json -s prod rollback $version
	ssh app@$dmip -p 49153 -- tail /home/app/harp/app/log/app.log
	# ssh app@$dmip -p 49153 -- cat /home/app/src/github.com/bom-d-van/harp/test/files/file1
	# ssh app@$dmip -p 49153 -- cat /home/app/src/github.com/bom-d-van/harp/test/files/file2
done

git checkout -- test/test_version.go test/files/file1 test/files/file2

# remove big file
rm test/files/big-file
rm test/files/test.swp
