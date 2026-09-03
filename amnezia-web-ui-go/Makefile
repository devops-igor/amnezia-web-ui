.PHONY: all build test test-race test-e2e lint gosec govulncheck security clean fmt vet check docker-build

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

test-e2e:
	./scripts/run_e2e.sh

lint:
	PATH=$$PATH:$$HOME/go/bin:/usr/local/go/bin golangci-lint run ./...

gosec:
	PATH=$$PATH:$$HOME/go/bin:/usr/local/go/bin gosec -quiet ./...

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
	docker build -t amnezia-web-panel:latest -f ../Dockerfile ..

docker-run:
	docker run --rm -p 5000:5000 --cap-add=NET_ADMIN --device=/dev/net/tun amnezia-web-panel:latest
