# Build
VERSION=`git describe --tags`
BUILD=`date +%FT%T%z`

# Binary names
BINARY_NAME=swkit
BINARY_TUI=swkit-tui
BINARY_raspberry=$(BINARY_NAME)_linux_armv7_$(VERSION)
BINARY_darwin=$(BINARY_NAME)_darwin_arm_$(VERSION)
BINARY_TUI_raspberry=$(BINARY_TUI)_linux_armv7_$(VERSION)
BINARY_TUI_darwin=$(BINARY_TUI)_darwin_arm_$(VERSION)

# Ld
LDFLAGS=-ldflags "-w -s -X main.Version=${VERSION} -X main.Build=${BUILD}"

# Basic go commands
GOCMD=go
GOBUILD=$(GOCMD) build $(LDFLAGS)


all: build-raspberry build-darwin

build:
	$(GOBUILD) -o $(BINARY_NAME) cmd/app/main.go

build-tui:
	$(GOBUILD) -o $(BINARY_TUI) cmd/tui/main.go

build-raspberry:
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o ./rel/$(BINARY_raspberry) cmd/app/main.go

build-darwin:
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o ./rel/$(BINARY_darwin) cmd/app/main.go

build-tui-raspberry:
	GOOS=linux GOARCH=arm GOARM=7 $(GOBUILD) -o ./rel/$(BINARY_TUI_raspberry) cmd/tui/main.go

build-tui-darwin:
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o ./rel/$(BINARY_TUI_darwin) cmd/tui/main.go

run-app:
	./$(BINARY_NAME)

run: build run-app

run-tui: build-tui
	./$(BINARY_TUI)

deploy: build-raspberry
ifndef HOST
	$(error HOST is required, e.g. make deploy HOST=pi@192.168.1.100)
endif
ifndef DEST
	$(error DEST is required, e.g. make deploy DEST=/home/pi)
endif
	scp ./rel/$(BINARY_raspberry) $(HOST):$(DEST)/$(BINARY_raspberry)
