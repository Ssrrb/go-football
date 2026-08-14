AIR_VERSION := v1.61.7

.PHONY: help dev run infra-up infra-down test test-go test-etl fmt vet build etl-discover etl-load-sofascore

help:
	@printf '%s\n' 'make dev           Run the complete local stack with live reload' \
		'make run           Run the Go service with .env and live reload' \
		'make infra-up      Start PostgreSQL and Redis' \
		'make test          Run Go and Python tests' \
		'make etl-discover  Run APF discovery locally' \
		'make etl-load-sofascore  Load the persisted SofaScore export'

dev:
	docker compose up --build

run:
	@set -a; [ ! -f .env ] || . ./.env; set +a; go run github.com/air-verse/air@$(AIR_VERSION) -c .air.toml

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

etl-load-sofascore:
	PYTHONPATH=etl/src python3 -m go_football_sources.load_sofascore_clausura_2026
