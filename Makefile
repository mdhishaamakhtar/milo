# Milo monorepo tasks. Run from the repo root.
.PHONY: up down gen lint test run live battery ios ios-test phone

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

battery:  ## Ask Milo about 60 real questions over a user's history in the local database: make battery AS=username (about 15 cents on Haiku)
	cd backend && MILO_BATTERY=1 MILO_BATTERY_USER=$(AS) go test ./internal/agent -run TestBattery -count=1 -v

ios:   ## Generate the Xcode project and open it
	cd ios && xcodegen && open Milo.xcodeproj

ios-test:  ## App tests on a simulator (SIM="iPhone 18 Pro" by default)
	cd ios && xcodegen -q && $(XCODEBUILD) test

# Device builds sign with MILO_TEAM_ID from .env and install on the first plugged-in iPhone.
TEAM ?= $(shell grep -s '^MILO_TEAM_ID=' .env | cut -d= -f2)
DEVICE ?= $(shell xcrun devicectl list devices 2>/dev/null | grep -Eo '[0-9A-F]{8}-[0-9A-F]{16}' | head -1)

phone:  ## Release build on your iPhone (a free Apple account needs this again every 7 days)
	cd ios && xcodegen -q && xcodebuild -project Milo.xcodeproj -scheme Milo -configuration Release \
		-destination 'id=$(DEVICE)' -derivedDataPath build/DerivedData \
		-allowProvisioningUpdates DEVELOPMENT_TEAM=$(TEAM) -quiet build
	xcrun devicectl device install app --device $(DEVICE) ios/build/DerivedData/Build/Products/Release-iphoneos/Milo.app
