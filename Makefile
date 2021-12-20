GOOS ?= $(shell uname -s | tr A-Z a-z)
GOCMD ?= go
GOBUILDFLAGS ?= -a -installsuffix cgo -ldflags="-s -w"

.PHONY: clean

clean:
	rm -rf build

build:
	CGO_ENABLED=0 $(GOCMD) build -o build/solana-ws-lb-$(GOOS) $(GOBUILDFLAGS) ./cmd

test:
	$(GOCMD) test $(GOTESTFLAGS) ./...
