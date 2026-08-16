.PHONY: build test run frontend docker

export PATH := $(HOME)/.local/go/bin:$(PATH)

build:
        cd backend && go build -o bin/server ./cmd/server

test:
        cd backend && go test ./...

run:
        cd backend && DEV_AUTH_USER=dev AUDIT_DB_PATH=../data/audit.db go run ./cmd/server

frontend:
        cd frontend && npm install && npm run build

docker:
        docker build -t k8up-gui:dev .

tidy:
        cd backend && go mod tidy
