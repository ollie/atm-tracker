APP_NAME := atm-tracker
APP_LABEL := Air Transport Magnate
APP_ID := cz.oldrichvetesnik.atm
BUILD_DIR := build
BINARY := $(BUILD_DIR)/$(APP_NAME)
MAIN_PKG := ./cmd/$(APP_NAME)
ICON := $(MAIN_PKG)/Icon.png
DIST_DIR := fyne-cross/dist

MAC_ARCH := $(shell go env GOARCH)
WINDOWS_ARCH := amd64
LINUX_ARCH := amd64

MAC_DIST := $(DIST_DIR)/darwin-$(MAC_ARCH)
WINDOWS_DIST := $(DIST_DIR)/windows-$(WINDOWS_ARCH)
LINUX_DIST := $(DIST_DIR)/linux-$(LINUX_ARCH)

MAC_ARCHIVE := $(MAC_DIST)/$(APP_LABEL)-mac-$(MAC_ARCH).zip
WINDOWS_ARCHIVE := $(WINDOWS_DIST)/$(APP_LABEL)-windows-$(WINDOWS_ARCH).zip
LINUX_ARCHIVE := $(LINUX_DIST)/$(APP_LABEL)-linux-$(LINUX_ARCH).tar.xz

VERSION_FILE := $(MAIN_PKG)/VERSION.txt
VERSION := $(strip $(shell cat $(VERSION_FILE) 2>/dev/null || echo dev))
# This doesn't need to change
BUILD_NUM ?= 1

META_FLAGS := -app-id $(APP_ID) -app-version $(VERSION) -app-build $(BUILD_NUM) \
	-name "$(APP_LABEL)" -icon $(ICON) -env GOTOOLCHAIN=auto

.PHONY: all test lint fmt run version build package package-mac package-windows package-linux package-all clean

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

version:
	@echo $(VERSION)

build: clean-build
	mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BINARY) $(MAIN_PKG)

package package-mac: build
	@command -v fyne >/dev/null || { echo "fyne not found: go install fyne.io/tools/cmd/fyne@latest"; exit 1; }
	fyne package --executable $(BINARY) --name "$(APP_LABEL)" --app-id $(APP_ID) \
		--app-version $(VERSION) --app-build $(BUILD_NUM) --icon $(ICON)
	rm -rf "$(MAC_DIST)/$(APP_LABEL).app" "$(MAC_ARCHIVE)"
	mkdir -p $(MAC_DIST)
	mv "$(APP_LABEL).app" $(MAC_DIST)/
	ditto -c -k --sequesterRsrc --keepParent \
		"$(MAC_DIST)/$(APP_LABEL).app" "$(MAC_ARCHIVE)"
	@echo "[✓] Package: \"$(CURDIR)/$(MAC_ARCHIVE)\""

package-windows: check-fyne-cross check-docker
	fyne-cross windows -arch=$(WINDOWS_ARCH) $(META_FLAGS) $(MAIN_PKG)
	mv "$(WINDOWS_DIST)/$(APP_LABEL).zip" "$(WINDOWS_ARCHIVE)"
	@echo "[✓] Package: \"$(CURDIR)/$(WINDOWS_ARCHIVE)\""

package-linux: check-fyne-cross check-docker
	fyne-cross linux -arch=$(LINUX_ARCH) $(META_FLAGS) $(MAIN_PKG)
	mv "$(LINUX_DIST)/$(APP_LABEL).tar.xz" "$(LINUX_ARCHIVE)"
	@echo "[✓] Package: \"$(CURDIR)/$(LINUX_ARCHIVE)\""

package-all: package-mac package-windows package-linux

clean: clean-build
	rm -rf fyne-cross

clean-build:
	rm -rf $(BUILD_DIR) "$(APP_LABEL).app"

.PHONY: clean-build check-fyne-cross check-docker

check-fyne-cross:
	@command -v fyne-cross >/dev/null || { echo "fyne-cross not found: go install github.com/fyne-io/fyne-cross@latest"; exit 1; }

check-docker:
	@docker info >/dev/null 2>&1 || { echo "docker daemon not running: start Docker Desktop"; exit 1; }
