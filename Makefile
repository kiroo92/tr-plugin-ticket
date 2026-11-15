TRPLUGIN ?= go tool trplugin
export GOMAXPROCS := 2
export GOFLAGS := -p=1
export GOMEMLIMIT := 1GiB
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
.NOTPARALLEL:
.PHONY: check frontend build pack
check:
	go test ./...
	go vet ./...
	pnpm --dir web test
frontend:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build
build:
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; binary=plugin; \
		if [ "$$os" = windows ]; then binary=plugin.exe; fi; \
		mkdir -p dist/$$platform/bin; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o dist/$$platform/bin/$$binary ./cmd/plugin; \
	done
pack: frontend check build
	$(TRPLUGIN) pack --manifest plugin.json --frontend dist/frontend --bin-dir dist --out release
	$(TRPLUGIN) validate release/*.tar.gz
