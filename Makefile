# Milo monorepo tasks. Run from the repo root.
.PHONY: up down gen lint test run live ios ios-test

# The simulator the app tests run on.
SIM ?= iPhone 18 Pro
XCODEBUILD = xcodebuild -project Milo.xcodeproj -scheme Milo -destination 'platform=iOS Simulator,name=$(SIM)'

up:    ## Start local Postgres (port 5433)
	docker compose up -d --wait

down:
	docker compose down

gen:   ## Regenerate API code for every client, and typed SQL for the server
	cd proto && buf lint && buf generate
	cd backend && sqlc generate

lint:
	cd proto && buf lint
	cd backend && go vet ./...

test:  ## Backend tests (Testcontainers starts Postgres; Docker must be running)
	cd backend && go test ./...

run:   ## Run the API on :8080 with the repo-root .env
	cd backend && go run ./cmd/api

live:  ## Milo against the real Claude API (needs a key in .env; costs about a cent)
	cd backend && MILO_LIVE=1 go test ./internal/agent/ -run TestLiveEvals -v

ios:   ## Generate the Xcode project and open it
	cd ios && xcodegen && open Milo.xcodeproj

ios-test:  ## App tests on a simulator (SIM="iPhone 18 Pro" by default)
	cd ios && xcodegen -q && $(XCODEBUILD) test
