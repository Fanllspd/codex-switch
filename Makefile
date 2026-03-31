BIN := bin/codex-switch

.PHONY: build test install

build:
	mkdir -p bin
	go build -o $(BIN) ./cmd/codex-switch

test:
	go test ./...

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(BIN) $(HOME)/.local/bin/codex-switch
