.PHONY: all build test test-race lint gosec govulncheck security clean fmt vet check docker-build docker-up docker-down

all: check build

build:
	$(MAKE) -C amnezia-web-ui-go build

test:
	$(MAKE) -C amnezia-web-ui-go test

test-race:
	$(MAKE) -C amnezia-web-ui-go test-race

lint:
	$(MAKE) -C amnezia-web-ui-go lint

gosec:
	$(MAKE) -C amnezia-web-ui-go gosec

govulncheck:
	$(MAKE) -C amnezia-web-ui-go govulncheck

security:
	$(MAKE) -C amnezia-web-ui-go security

fmt:
	$(MAKE) -C amnezia-web-ui-go fmt

vet:
	$(MAKE) -C amnezia-web-ui-go vet

clean:
	$(MAKE) -C amnezia-web-ui-go clean

check:
	$(MAKE) -C amnezia-web-ui-go check

docker-build:
	docker build -t amnezia-web-panel:latest .

docker-up:
	docker compose up -d

docker-down:
	docker compose down
