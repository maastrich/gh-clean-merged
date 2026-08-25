.PHONY: build clean install test lint

# Build the extension binary. The name must match the repository name for
# `gh extension install` to pick it up.
build:
	go build -o gh-clean-merged

clean:
	rm -f gh-clean-merged gh-clean-merged-*

# Install the local build as a gh extension
install: build
	gh extension install .

test:
	go test ./...

lint:
	gofmt -l . && go vet ./...

build-all: clean
	GOOS=linux GOARCH=amd64 go build -o gh-clean-merged-linux-amd64
	GOOS=linux GOARCH=arm64 go build -o gh-clean-merged-linux-arm64
	GOOS=darwin GOARCH=amd64 go build -o gh-clean-merged-darwin-amd64
	GOOS=darwin GOARCH=arm64 go build -o gh-clean-merged-darwin-arm64
	GOOS=windows GOARCH=amd64 go build -o gh-clean-merged-windows-amd64.exe

all: build
