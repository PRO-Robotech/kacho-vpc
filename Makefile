BINARY         := kacho-vpc
CMD            := ./cmd/vpc
# KAC-96 (skill evgeniy §9 K.1, AP-9): отдельный binary мигратора.
MIGRATOR_BIN   := kacho-migrator
MIGRATOR_CMD   := ./cmd/migrator
IMAGE          := kacho-vpc:dev

.PHONY: build build-migrator test test-short vet lint docker sync-migrations generate

build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) $(CMD)

build-migrator:
	CGO_ENABLED=0 go build -o bin/$(MIGRATOR_BIN) $(MIGRATOR_CMD)

test:
	go test ./... -race -cover -timeout 300s

test-short:
	go test ./... -race -cover -short -timeout 120s

vet:
	go vet ./...

lint:
	golangci-lint run ./...

sync-migrations:
	cp ../kacho-corelib/migrations/common/*.sql migrations/
	cp ../kacho-corelib/migrations/common/*.sql internal/migrations/

docker:
	cd .. && docker build -f kacho-vpc/Dockerfile -t $(IMAGE) .

.PHONY: migrate-up migrate-down migrate-status
# KAC-96: migrate-* теперь дёргают отдельный binary `bin/kacho-migrator`.
# Зависимость на build-migrator гарантирует, что bin/ актуальный.
migrate-up: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) up

migrate-down: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) down

migrate-status: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) status
