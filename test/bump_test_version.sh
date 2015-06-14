current=$(cat $GOPATH/src/github.com/bom-d-van/harp/test/test_version.go | grep -o "[0-9]\{1,\}")
current=$(expr $current + 1)

cat <<EOF > $GOPATH/src/github.com/bom-d-van/harp/test/test_version.go
package main

const version = $current
EOF

git add $GOPATH/src/github.com/bom-d-van/harp/version.go
