.PHONY: web build test run

web:
	npm --prefix web ci
	npm --prefix web run build

build: web
	go build -o deepflow ./cmd/deepflow

test:
	go test ./...
	npm --prefix web test

run: build
	./deepflow
