# Milo monorepo tasks. Run from the repo root.
.PHONY: up down gen lint test run

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
