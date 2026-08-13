.PHONY: help dev run infra-up infra-down test test-go test-etl fmt vet build etl-discover

help:
	@printf '%s\n' 'make dev           Run the complete local stack' \
		'make run           Run the Go service with .env' \
		'make infra-up      Start PostgreSQL and Redis' \
		'make test          Run Go and Python tests' \
		'make etl-discover  Run APF discovery locally'

dev:
	docker compose up --build

run:
	@set -a; [ ! -f .env ] || . ./.env; set +a; go run ./cmd/web

infra-up:
	docker compose up -d postgres redis

infra-down:
	docker compose down

test: test-go test-etl

test-go:
	go test ./...

test-etl:
	PYTHONPATH=etl/src python3 -m unittest discover -s etl/tests -v

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

vet:
	go vet ./...

build:
	go build ./cmd/web

etl-discover:
	PYTHONPATH=etl/src python3 -m go_football_sources.cli discover
