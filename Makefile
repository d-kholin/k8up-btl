.PHONY: build test run frontend docker docker-push tidy

export PATH := $(HOME)/.local/go/bin:$(PATH)

IMAGE ?= ghcr.io/d-kholin/k8up-btl
TAG ?= dev

build:
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

test:
	cd backend && go test ./...

run:
	cd backend && DEV_AUTH_USER=dev AUDIT_DB_PATH=../data/audit.db go run ./cmd/server

frontend:
	cd frontend && npm ci && npm run build

docker:
	docker build -t $(IMAGE):$(TAG) .

docker-push: docker
	docker push $(IMAGE):$(TAG)

tidy:
	cd backend && go mod tidy
