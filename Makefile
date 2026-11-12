# Milo monorepo tasks. Run from the repo root.
.PHONY: up down gen lint test cover run live battery ios ios-test phone

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

# Branch coverage of the server, per package. Go's own -cover counts statements, so gobco
# (run with go run, nothing in go.mod) counts both outcomes of every condition. It tests one
# package at a time from a copy of the module, so the maths vectors are passed by path.
GOBCO = go run github.com/rillig/gobco@v1.3.4
COVER_PKGS = ./cmd/api ./internal/agent ./internal/auth ./internal/config ./internal/db ./internal/maths ./internal/rows ./internal/server ./internal/sync
# The app's logic: everything but views, HealthKit and Speech. Swift's coverage has no branch
# counters, so these are regions: each if and else body, switch case and side of && or ??.
APP_LOGIC = Milo/Data/*.swift Milo/Domain/*.swift Milo/App/AppModel.swift Milo/Features/Milo/MiloSession.swift \
	Milo/Features/Workout/WorkoutModel.swift Milo/Features/Workout/RestTimer.swift
APP_BUILD = build/DerivedData/Build
APP_COV = xcrun llvm-cov report -instr-profile $(APP_BUILD)/ProfileData/*/Coverage.profdata $(APP_BUILD)/Products/Debug-iphonesimulator/Milo.app/Milo.debug.dylib

cover:  ## Branch coverage of the server and region coverage of the app's logic
	@echo "Server, branches:"
	@cd backend && for p in $(COVER_PKGS); do \
		MILO_TESTDATA=$(CURDIR)/testdata $(GOBCO) -branch $$p | sed -n "s|^Branch coverage: |  $$p |p"; \
	done | awk '{print; split($$2, f, "/"); c += f[1]; n += f[2]} END {printf "  total %d/%d = %.1f%%\n", c, n, 100 * c / n}'
	@cd ios && xcodegen -q && $(XCODEBUILD) -derivedDataPath build/DerivedData -enableCodeCoverage YES test -quiet > /dev/null 2>&1 \
		|| { echo "App tests failed: run make ios-test to see why"; exit 1; }
	@echo "App, regions:"
	@cd ios && $(APP_COV) $(APP_LOGIC) | awk '/^TOTAL/ {print "  logic " $$4}'
	@cd ios && $(APP_COV) Milo | awk '/^TOTAL/ {print "  whole app, views included " $$4}'

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
