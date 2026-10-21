.PHONY: help check fmt vet test race build frontend run loadtest clean

BINARY := comic
LDFLAGS := -s -w

help:
	@printf '%s\n' \
	  'make frontend   build the SPA into web/dist (embedded next)' \
	  'make build      build the local binary with the SPA embedded' \
	  'make run        run it on :8080' \
	  'make check      gofmt + vet + race tests (what CI should run)' \
	  'make race       tests only, with the race detector' \
	  'make loadtest   300 connections against a local server' \
	  'make clean      remove build output'

# The embed directive needs web/dist to exist, so the frontend comes first.
frontend:
	cd web && pnpm install --frozen-lockfile && pnpm build
	@# vite empties outDir, which deletes the committed placeholder that keeps
	@# `//go:embed all:web/dist` valid in a fresh clone. Put it back (empty, so the
	@# working tree stays clean).
	@mkdir -p web/dist && : > web/dist/.gitkeep

build: frontend
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) .

run: build
	./$(BINARY)

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race -count=1 ./...

check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt found unformatted files" && exit 1)
	go vet ./...
	go test -race -count=1 ./...
	@# React hooks rules catch the class of bug that caused an infinite reconnect
	@# loop: an effect that writes state listed in its own dependency array.
	cd web && ./node_modules/.bin/oxlint

loadtest:
	go run ./cmd/loadtest -conns 300 -rate 2 -duration 10s -rampup 5s

clean:
	rm -f $(BINARY) server-linux
