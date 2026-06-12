BINARY := gardynd
PKG := ./cmd/gardynd

.PHONY: build build-pi test tidy

build:
	go build -o bin/$(BINARY) $(PKG)

# Stock Pi Zero is ARMv6, 32-bit. CGO off for a static binary.
build-pi:
	GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build -trimpath -o bin/$(BINARY)-armv6 $(PKG)

test:
	go test ./...

tidy:
	go mod tidy
