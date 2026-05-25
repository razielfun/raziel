.PHONY: build test lint clean run

BINARY = raziel
PKG    = ./cmd/raziel

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY) $(PKG)

build-all:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o $(BINARY)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY)-windows-amd64.exe $(PKG)

test:
	go test ./...

test-verbose:
	go test -v ./...

lint:
	go vet ./...

clean:
	rm -f $(BINARY) $(BINARY)-*

run:
	RAZIEL_API_SECRET=dev-secret go run $(PKG) server

tidy:
	go mod tidy
