# --- project config ---
APP       := pushdalek
CMD_BOT   := ./cmd/bot
CMD_SYNC  := ./cmd/sync
CMD_VKCHK := ./cmd/vkcheck

BIN_DIR   := bin
BIN_BOT   := $(BIN_DIR)/$(APP)

# docker
IMAGE     := $(APP):local
# DATA_DIR  := data

SHELL := /bin/bash

.PHONY: help tidy fmt vet test build clean run run-bot sync vkcheck \
        docker-build docker-run docker-shell

help:
	@echo "Targets:"
	@echo "  tidy          - go mod tidy"
	@echo "  fmt           - gofmt -w"
	@echo "  vet           - go vet"
	@echo "  test          - go test"
	@echo "  build         - build bot binary -> $(BIN_BOT)"
	@echo "  run           - run bot (loads .env if exists)"
	@echo "  sync          - run cmd/sync (loads .env if exists)"
	@echo "  vkcheck       - run cmd/vkcheck (loads .env if exists)"
	@echo "  docker-build  - build docker image -> $(IMAGE)"
	@echo "  docker-run    - run docker container with .env + persistent ./$(DATA_DIR)"
	@echo "  clean         - remove $(BIN_DIR)"

tidy:
	go mod tidy

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

build-linux:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN_BOT) $(CMD_BOT)

build-mac:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN_BOT) $(CMD_BOT)

clean:
	rm -rf $(BIN_DIR)

# --- local run helpers ---
# Загружает .env в текущий процесс bash и запускает команду
define RUN_WITH_ENV
	set -euo pipefail; \
	if [[ -f .env ]]; then set -a; source .env; set +a; fi; \
	$(1)
endef

run:
	$(call RUN_WITH_ENV,go run $(CMD_BOT))

sync:
	$(call RUN_WITH_ENV,go run $(CMD_SYNC))

vkcheck:
	$(call RUN_WITH_ENV,go run $(CMD_VKCHK))

# --- docker ---
docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -it \
	  --env-file .env \
	  -v "./:/data" \
	  -e DB_PATH=/data/bot.db \
	  $(IMAGE)

