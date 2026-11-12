# Common development tasks. `make test` is the full check: it starts the
# Docker database so the API tests run instead of being skipped.

DB_PORT ?= 5432
TEST_DATABASE_URL ?= postgres://travel:travel@localhost:$(DB_PORT)/travel_docs_test?sslmode=disable

.PHONY: test test-unit contract-update lint frontend-check seed db-dump db-restore scrape scraper-test

test:
	docker compose up -d --wait db
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./...

test-unit:
	go test -count=1 ./...

# Regenerate the API contract golden files after an intended API change.
contract-update:
	docker compose up -d --wait db
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./apitest -run Contract -count=1 -update

lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

frontend-check:
	cd frontend && npm run lint && npx tsc --noEmit && npm test && npm run build

# Sample routes from github.com/gps-touring/sample-gpx for the demo account
# (needs a running API; see cmd/seed for options).
seed:
	go run ./cmd/seed

# The route scraper (scraper/, a separate service): one run against the
# local API. Options: cd scraper && go run . -h
db-dump: ## Dump the database and scraper state to dumps/ (to load elsewhere with db-restore)
	scripts/db-dump.sh

db-restore: ## Replace the database with a dump: make db-restore FILE=dumps/....tar.gz
	scripts/db-restore.sh "$(FILE)"

scrape:
	cd scraper && go run .

scraper-test:
	cd scraper && go vet ./... && go test -race -count=1 ./...
