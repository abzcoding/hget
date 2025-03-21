COMMIT := $(shell git describe --always)
BINARY := hget
BINDIR := bin
INSTALL_PATH := /usr/local/bin

.PHONY: all clean build install test deps

all: build

deps:
	@echo "====> Updating dependencies..."
	go mod tidy

clean:
	@echo "====> Removing installed binary"
	rm -f $(BINDIR)/$(BINARY)

test:
	@echo "====> Running tests..."
	go test -v ./...

build: deps
	@echo "====> Building $(BINARY) in ./$(BINDIR)"
	mkdir -p $(BINDIR)
	go build -ldflags "-X main.GitCommit=\"$(COMMIT)\"" -o $(BINDIR)/$(BINARY)

install: build
	@echo "====> Installing $(BINARY) in $(INSTALL_PATH)/$(BINARY)"
	chmod +x ./$(BINDIR)/$(BINARY)
	sudo mv ./$(BINDIR)/$(BINARY) $(INSTALL_PATH)/$(BINARY)
