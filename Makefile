# Build
VERSION=`git describe --tags`
BUILD=`date +%FT%T%z`

# Binary names
BINARY_NAME=swkit
BINARY_raspberry=$(BINARY_NAME)_linux_armv7_$(VERSION)
BINARY_darwin=$(BINARY_NAME)_darwin_arm_$(VERSION)

# Ld
LDFLAGS=-ldflags "-w -s -X main.Version=${VERSION} -X main.Build=${BUILD}"

# Basic go commands
GOCMD=go
GOBUILD=$(GOCMD) build $(LDFLAGS)


all: build-raspberry build-darwin

build:
	$(GOBUILD) -o $(BINARY_NAME)


build-raspberry:
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o ./rel/$(BINARY_raspberry) cmd/app/main.go

build-darwin:
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o ./rel/$(BINARY_darwin) cmd/app/main.go

run-app:
	./$(BINARY_NAME)

run: build run-app
