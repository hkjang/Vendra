.PHONY: dev test build image offline-release

VERSION ?= 0.6.14
IMAGE ?= vendra:v$(VERSION)

dev:
	go run ./cmd/vendra

test:
	go test ./cmd/... ./internal/...
	go vet ./cmd/... ./internal/...
	cd web && npm run build

build:
	mkdir -p dist
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/hkjang/Vendra/internal/httpapi.Version=$(VERSION)" -o dist/vendra ./cmd/vendra

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo unknown) --build-arg BUILD_TIME=$$(date -u +%FT%TZ) -t $(IMAGE) .

offline-release: image
	mkdir -p dist
	docker save $(IMAGE) | gzip -9 > dist/vendra-v$(VERSION).tar.gz
