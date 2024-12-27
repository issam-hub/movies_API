include .env

.PHONY: run
run: build
	@./bin/api

.PHONY: audit
audit: vendor
	@echo "-> Formatting code..."
	@go fmt ./...
	@echo "-> Vetting code..."
	@go vet ./...
	staticcheck ./...
	@echo "-> Running tests"
	go test -race -vet=off ./...

.PHONY: vendor
vendor:
	@echo "-> Tidying and Verifying module dependencies..."
	@go mod tidy
	@go mod verify
	@echo "-> Vendoring dependencies..."
	@go mod vendor


current_time = $(shell date --iso-8601=seconds)
.PHONY: build
build:
	@echo "-> Building..."
	@go build -ldflags='-s -X main.buildTime=${current_time}' -o ./bin/api ./cmd/api/
	GOOS=linux GOARCH=amd64 go build -ldflags='-s -X main.buildTime=${current_time}' -o=./bin/linux_amd64/api ./cmd/api/

.PHONY: migrate
migrate:
	@echo "-> Creating migration files..."
	migrate create -seq -ext=.sql -dir=./migrations $$name


MIGRATE_PATH := migrate -path=./migrations -database=${DSN}

.PHONY: migrateRun
migrateRun:
	@echo "-> Running up migrations..."
	@$(MIGRATE_PATH) up

.PHONY: migrateReset
migrateReset:
	@echo "-> Running down migrations..."
	@$(MIGRATE_PATH) down

.PHONY: migrateGoto
migrateGoto:
	@echo "-> Running goto migrations..."
	@$(MIGRATE_PATH) goto $$version

.PHONY: migrateForce
migrateForce:
	@echo "-> Running force migrations..."
	@$(MIGRATE_PATH) force $$version


