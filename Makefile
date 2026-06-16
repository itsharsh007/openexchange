COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: help up infra down seed test logs colima-start colima-stop fmt

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

colima-start: ## Start a capped, cool-running container VM (macOS)
	colima start --cpu 4 --memory 6

colima-stop: ## Stop the container VM and reclaim CPU/RAM
	colima stop

infra: ## Start backing services only (Postgres, Redis, Kafka)
	$(COMPOSE) up -d postgres redis kafka

up: ## Start the full stack
	$(COMPOSE) up -d

down: ## Stop everything
	$(COMPOSE) down

logs: ## Tail logs from all services
	$(COMPOSE) logs -f

seed: ## Create demo accounts/symbols and simulated order flow
	./scripts/seed.sh

test: ## Run every service's test suite
	cd engine && ./gradlew test
	cd gateway && go test ./...
	cd risk && pytest -q
	cd web && npm test --silent

fmt: ## Format code in every service
	cd engine && ./gradlew spotlessApply || true
	cd gateway && go fmt ./...
	cd risk && ruff format . || true
	cd web && npm run format || true
