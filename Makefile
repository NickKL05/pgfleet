# pgfleet developer tasks. The CLI is the primary artifact; the `web`/`dashboard`
# targets build and run the optional embedded dashboard.

BINARY  := pgfleet
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build web all test vet fmt lint dashboard docker demo clean

# build compiles the binary, embedding whatever is currently in
# internal/web/dist (the committed pre-built UI, or a fresh `make web`).
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/pgfleet

# web builds the Vue app into internal/web/dist so `build` embeds a fresh bundle.
# Requires Node; the committed dist means this is optional for a plain build.
web:
	cd web && (test -f package-lock.json && npm ci || npm install)
	cd web && npm run build

# all rebuilds the UI and then the binary — the full "single binary with fresh
# UI" produced without Docker.
all: web build

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# dashboard runs the embedded UI locally against $PGFLEET_DSN (default: the demo
# compose stack). Migrate the fleet first for a populated view.
dashboard: build
	PGFLEET_DSN=$${PGFLEET_DSN:-postgres://pgfleet:pgfleet@localhost:5432/fleet} ./$(BINARY) web --addr :8080

# docker builds the multi-stage image (Node build -> Go build -> distroless).
docker:
	docker build -t pgfleet:$(VERSION) .

# demo runs the scripted 250-tenant walkthrough.
demo:
	./demo/demo.sh

clean:
	rm -f $(BINARY) $(BINARY).exe coverage.out coverage.html
	rm -rf repair/
