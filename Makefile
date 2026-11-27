VERSION ?= 0.0.0-dev
RELEASE_TAG ?=
LDFLAGS = -X ezpanel/internal/version.Version=$(VERSION) -X ezpanel/internal/version.ReleaseTag=$(RELEASE_TAG)
FRONTEND_DIR = web
FRONTEND_INDEX = $(FRONTEND_DIR)/dist/index.html

.PHONY: dev init-dev build test lint frontend check-node

check-node:
	@node -e "if (process.versions.node.split('.')[0] !== '24') { console.error('请先使用 Node 24 LTS（nvm use）'); process.exit(1) }"

frontend: check-node
	cd $(FRONTEND_DIR) && if [ ! -d node_modules ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then npm ci; fi && npm run build
	test -s $(FRONTEND_INDEX)
	test -d $(FRONTEND_DIR)/dist/assets

build: frontend
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ezpanel-linux-amd64 ./cmd/ezpanel
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ezpanel-linux-arm64 ./cmd/ezpanel
	go run ./scripts/release-hashes dist

init-dev: check-node
	@if [ ! -s $(FRONTEND_INDEX) ]; then $(MAKE) frontend; fi
	mkdir -p dist
	go build -o dist/ezpanel ./cmd/ezpanel
	./dist/ezpanel server --dev --install-dir "$(CURDIR)/.dev-panel" --init

dev: check-node
	@if [ ! -s $(FRONTEND_INDEX) ]; then $(MAKE) frontend; fi
	mkdir -p dist
	go build -o dist/ezpanel ./cmd/ezpanel
	@set -e; target=$$(./dist/ezpanel info --dev --install-dir "$(CURDIR)/.dev-panel" | sed -n 's/^监听: /http:\/\//p'); test -n "$$target"; ./dist/ezpanel server --dev --install-dir "$(CURDIR)/.dev-panel" & backend=$$!; trap 'kill $$backend 2>/dev/null || true' EXIT INT TERM; cd $(FRONTEND_DIR) && PANEL_DEV_TARGET="$$target" npm run dev

test:
	@if [ ! -s $(FRONTEND_INDEX) ]; then $(MAKE) frontend; fi
	go test ./...

lint: check-node
	@if [ ! -s $(FRONTEND_INDEX) ]; then $(MAKE) frontend; fi
	go vet ./...
	cd $(FRONTEND_DIR) && npm run lint
