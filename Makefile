# Go is built and tested inside the golang:1.24 image, so it need not be
# installed locally. Module and build caches live in named Docker volumes.
GO_IMAGE ?= golang:1.24
VERSION  ?= 0.1.0-dev
GO = docker run --rm -v "$(CURDIR)":/src -v tidyfleet-gomod:/go/pkg/mod -v tidyfleet-gocache:/root/.cache/go-build \
	-e GOTOOLCHAIN=local -e CGO_ENABLED=0 $(GO_ENV) -w /src/$(1) $(GO_IMAGE)
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: help try check cleaner-ui cleaner agent agent-darwin agent-all install-agent test test-agent test-server vet dashboard up down logs create-org clean

help:
	@echo "make try            start everything and enroll this Mac (one command)"
	@echo "make check          run every automated check: Go vet + tests, both Next.js apps"
	@echo "make cleaner        build and open the cleaner app (disk tree, preview, clean)"
	@echo "make agent-darwin   build bin/tidyfleet-darwin-arm64 and -amd64"
	@echo "make agent-all      build the agent for macOS, Windows and Linux"
	@echo "make install-agent  install the agent + LaunchAgent on this Mac"
	@echo "make test           run agent and server tests (server tests use a throwaway Postgres)"
	@echo "make up / down      start or stop the local stack (docker compose)"
	@echo "make create-org NAME='Acme' EMAIL=you@acme.test"

try:
	./scripts/try-on-mac.sh

agent: agent-darwin

# The cleaner UI is a Next.js static export embedded into the agent binary.
UI_STATIC = agent/internal/ui/static
cleaner-ui:
	cd cleaner-ui && ([ -d node_modules ] || npm ci) && npm run build
	find $(UI_STATIC) -mindepth 1 ! -name .keep -exec rm -rf {} +
	cp -R cleaner-ui/out/. $(UI_STATIC)/

cleaner: cleaner-ui agent-darwin
	./bin/tidyfleet-darwin-$$(uname -m | sed 's/x86_64/amd64/') ui

agent-darwin: $(UI_STATIC)/index.html
	$(call GO,agent) sh -c 'GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/tidyfleet-darwin-arm64 ./cmd/tidyfleet && \
		GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/tidyfleet-darwin-amd64 ./cmd/tidyfleet'

agent-all: agent-darwin
	$(call GO,agent) sh -c 'GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/tidyfleet-windows-amd64.exe ./cmd/tidyfleet && \
		GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/tidyfleet-linux-amd64 ./cmd/tidyfleet'

install-try:
	./scripts/try-on-mac.sh

agent: agent-darwin

# The cleaner UI is a Next.js static export embedded into the agent binary.
UI_STATIC = agent/internal/ui/static
cleaner-ui:
	cd cleaner-ui && ([ -d node_modules ] || npm ci) && npm run build
	find $(UI_STATIC) -mindepth 1 ! -name .keep -exec rm -rf {} +
	cp -R cleaner-ui/out/. $(UI_STATIC)/

cleaner: cleaner-ui agent-darwin
	./bin/tidyfleet-darwin-$$(uname -m | sed 's/x86_64/amd64/') ui
	./packaging/macos/install.sh

vet:
	$(call GO,agent) sh -c 'go vet ./... && GOOS=darwin go vet ./... && GOOS=windows go vet ./...'
	$(call GO,server) go vet ./...

test: test-agent test-server

# Everything CI would run: Go vet (3 OSes) and tests, plus type checks and
# production builds of the dashboard and the cleaner app.
check: vet test
	cd dashboard && ([ -d node_modules ] || npm ci) && npx tsc --noEmit && npm run build
	cd cleaner-ui && ([ -d node_modules ] || npm ci) && npx tsc --noEmit && npm run build
	@echo "All checks passed."

test-agent:
	$(call GO,agent) go test -count=1 ./...

test-server:
	@docker network create tidyfleet-test >/dev/null 2>&1 || true
	@docker rm -f tidyfleet-test-db >/dev/null 2>&1 || true
	docker run -d --name tidyfleet-test-db --network tidyfleet-test -e POSTGRES_PASSWORD=test postgres:16-alpine >/dev/null
	@until docker exec tidyfleet-test-db pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	$(call GO,server) go test -count=1 ./... ; \
		status=$$?; docker rm -f tidyfleet-test-db >/dev/null; exit $$status

test-server: GO = docker run --rm --network tidyfleet-test -v "$(CURDIR)":/src -v tidyfleet-gomod:/go/pkg/mod -v tidyfleet-gocache:/root/.cache/go-build \
	-e GOTOOLCHAIN=local -e 'TIDYFLEET_TEST_DATABASE_URL=postgres://postgres:test@tidyfleet-test-db:5432/postgres?sslmode=disable' -w /src/$(1) $(GO_IMAGE)

dashboard:
	cd dashboard && npm ci && npm run build

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f server dashboard

create-org:
	docker compose exec server tidyfleet-server create-org -name "$(NAME)" -admin-email "$(EMAIL)"

clean:
	rm -rf bin dashboard/.next cleaner-ui/.next cleaner-ui/out
	find $(UI_STATIC) -mindepth 1 ! -name .keep -exec rm -rf {} +
