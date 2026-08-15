default: fmt lint build test

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

# Acceptance tests run against a real hub. Required environment:
#   TF_ACC=1
#   IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net
#   plus credentials for one auth mode (Azure CLI login / ARM_* / IOTHUB_CONNECTION_STRING).
testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: default build install lint generate fmt test testacc
