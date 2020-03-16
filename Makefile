all: fmt build

build:
	go build ./...
fmt:
	go fmt ./...

