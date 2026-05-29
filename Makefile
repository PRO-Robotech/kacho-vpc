BINARY         := kacho-vpc
CMD            := ./cmd/vpc
# KAC-96 (skill evgeniy §9 K.1, AP-9): отдельный binary мигратора.
MIGRATOR_BIN   := kacho-migrator
MIGRATOR_CMD   := ./cmd/migrator
IMAGE          := kacho-vpc:dev

.PHONY: build build-migrator test test-short vet lint docker sync-migrations generate audit-list-filter

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

# audit-list-filter — RBAC v2 / KAC-219 / W6 CI gate.
# Refuses to ship a public List<Resource> handler without listauthz wiring.
# Whitelist admin-only handlers via --allow=<resource>.
audit-list-filter:
	@./tools/audit-list-filter.sh --allow=addresspool

lint:
	golangci-lint run ./...

sync-migrations:
	@echo "sync-migrations is a no-op after the migration squash (chore/squash-migrations)."
	@echo "The operations table is now inline in internal/migrations/0001_initial.sql,"
	@echo "schema kacho_vpc. Re-copying corelib's common/0001_operations.sql would"
	@echo "create a conflicting unqualified 'operations' table in public schema."

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
