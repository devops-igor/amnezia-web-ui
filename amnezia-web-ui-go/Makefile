.PHONY: all build test test-race lint gosec govulncheck security clean fmt vet check docker-build

APP_NAME := panel
BIN_DIR := bin
CMD_DIR := cmd/panel

all: check build

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(APP_NAME) ./$(CMD_DIR)

test:
	go test -v ./...

test-race:
	go test -race -v ./...

lint:
	PATH=$$PATH:$$HOME/go/bin:/usr/local/go/bin golangci-lint run ./...

gosec:
	PATH=$$PATH:$$HOME/go/bin:/usr/local/go/bin gosec ./...

govulncheck:
	-PATH=$$PATH:$$HOME/go/bin:/usr/local/go/bin govulncheck ./...

security: gosec govulncheck

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)

check: fmt vet lint security test-race

docker-build:
	docker build -t amnezia-web-panel:latest .
