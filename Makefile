.PHONY: build

build:
	go build -buildmode=plugin -o script-docker.so script.go
