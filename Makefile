.PHONY: build run test test-short lint tidy clean help

BINARY_NAME := gopi-pro
BUILD_DIR := build
GO_MAIN := ./cmd/gopi-pro/

ifeq ($(OS),Windows_NT)
SHELL := cmd.exe
.SHELLFLAGS := /C

BINARY_EXT := .exe
BUILD_OUTPUT := $(BUILD_DIR)/$(BINARY_NAME)$(BINARY_EXT)
RUN_OUTPUT := $(BUILD_DIR)\$(BINARY_NAME)$(BINARY_EXT)

MKDIR_CMD := if not exist "$(BUILD_DIR)" mkdir "$(BUILD_DIR)"
CLEAN_CMD := if exist "$(BUILD_DIR)" rmdir /S /Q "$(BUILD_DIR)"
else
BINARY_EXT :=
BUILD_OUTPUT := $(BUILD_DIR)/$(BINARY_NAME)$(BINARY_EXT)
RUN_OUTPUT := ./$(BUILD_OUTPUT)

MKDIR_CMD := mkdir -p "$(BUILD_DIR)"
CLEAN_CMD := rm -rf "$(BUILD_DIR)"
endif

build:
	@echo 构建 gopi-pro...
	@$(MKDIR_CMD)
	go build -ldflags="-s -w" -o "$(BUILD_OUTPUT)" $(GO_MAIN)
	@echo 构建完成: $(BUILD_OUTPUT)

run: build
	$(RUN_OUTPUT)

test:
	@echo 运行单元测试...
	go test ./... -v -timeout 30s

test-short:
	@echo 运行快速测试...
	go test ./... -short -timeout 10s

lint:
	@echo 运行 go vet...
	go vet ./...

tidy:
	@echo 整理依赖...
	go mod tidy

clean:
	@echo 清理构建目录...
	@$(CLEAN_CMD)

help:
	@echo 可用目标:
	@echo   make build      - 构建二进制到 $(BUILD_OUTPUT)
	@echo   make run        - 构建并运行
	@echo   make test       - 运行全部测试
	@echo   make test-short - 运行快速测试
	@echo   make lint       - 运行 go vet
	@echo   make tidy       - 整理 go.mod/go.sum
	@echo   make clean      - 清理构建产物

.DEFAULT_GOAL := build
