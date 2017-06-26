DIR=$(cd ../; pwd)
export GOPATH=$GOPATH:$DIR
GOOS=linux GOARCH=amd64 go build qufop.go
