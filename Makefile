APP_NAME := atm-tracker
BUILD_DIR := build
BINARY := $(BUILD_DIR)/$(APP_NAME)
MAIN_PKG := ./cmd/$(APP_NAME)

.PHONY: all test lint fmt run build package clean

all: build

test: lint
	go test -race ./... && go vet ./...

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not found: brew install golangci-lint"; exit 1; }
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

run:
	go run $(MAIN_PKG)

build: clean
	mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BINARY) $(MAIN_PKG)

# a double-clickable .app bundle, reading the metadata from FyneApp.toml
package:
	@command -v fyne >/dev/null || { echo "fyne not found: go install fyne.io/tools/cmd/fyne@latest"; exit 1; }
	fyne package --src $(MAIN_PKG)

clean:
	rm -rf $(BUILD_DIR)
