BINARY := aero-arc-api
CMD := ./cmd/aero-arc-api
BIN_DIR := bin
BIN := $(BIN_DIR)/$(BINARY)

.PHONY: help build run run-demo test integration fmt vet tidy check clean

help:
	@printf "Targets:\n"
	@printf "  make build  Build $(BIN)\n"
	@printf "  make run    Run the API locally\n"
	@printf "  make run-demo Run the API locally with demo dashboard data\n"
	@printf "  make test   Run all tests\n"
	@printf "  make integration Run integration tests (requires Docker)\n"
	@printf "  make fmt    Format Go files\n"
	@printf "  make vet    Run go vet\n"
	@printf "  make tidy   Tidy go.mod and go.sum\n"
	@printf "  make check  Run fmt, vet, and test\n"
	@printf "  make clean  Remove build artifacts\n"

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) $(CMD)

run:
	go run $(CMD) start

run-demo:
	go run $(CMD) start --seed demo

test:
	go test ./...

integration:
	go test -tags=integration ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

check: fmt vet test

clean:
	rm -rf $(BIN_DIR)
