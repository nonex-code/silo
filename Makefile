BINARY := silo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w
GOFLAGS :=

.PHONY: all build test vet fmt release clean

all: build

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	GOPROXY=https://goproxy.cn,direct go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Cross compile static binaries into dist/.
release:
	@mkdir -p dist
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-arm64.exe .
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 .

clean:
	rm -rf dist $(BINARY) $(BINARY).exe