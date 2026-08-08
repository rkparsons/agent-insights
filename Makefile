BIN := $(HOME)/.local/bin/agent-insights

.PHONY: build install test clean

build:
	go build -o $(BIN) ./cmd/agent-insights

install: build
	@echo "Binary installed at $(BIN)"

test:
	go test ./...

clean:
	rm -f $(BIN)
