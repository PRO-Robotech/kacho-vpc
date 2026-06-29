# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

BINARY         := kacho-vpc
CMD            := ./cmd/vpc
# Миграции исполняет отдельный binary мигратора (не subcommand основного сервера).
MIGRATOR_BIN   := kacho-migrator
MIGRATOR_CMD   := ./cmd/migrator
IMAGE          := kacho-vpc:dev

.PHONY: build build-migrator test test-short vet lint docker sync-migrations audit-list-filter proto-install-plugins proto-vendor proto-lint proto-gen

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

# audit-list-filter — CI-гейт listauthz.
# Refuses to ship a public List<Resource> handler without listauthz wiring.
# Whitelist admin-only handlers via --allow=<resource>.
audit-list-filter:
	@./tools/audit-list-filter.sh --allow=addresspool

lint:
	golangci-lint run ./...

sync-migrations:
	@echo "sync-migrations is a no-op after the migration squash baseline."
	@echo "The operations table is now inline in internal/migrations/0001_initial.sql,"
	@echo "schema kacho_vpc. Re-copying corelib's common/0001_operations.sql would"
	@echo "create a conflicting unqualified 'operations' table in public schema."

docker:
	cd .. && docker build -f kacho-vpc/Dockerfile -t $(IMAGE) .

# proto-install-plugins — ставит protoc-плагины в $GOBIN (lookup через $PATH для buf).
# Доменный proto vpc генерируется этими тремя плагинами; permission-catalog для vpc —
# hand-written (internal/apps/kacho/check/permission_map.go), buf-catalog-плагин не нужен.
proto-install-plugins:
	go install google.golang.org/protobuf/cmd/protoc-gen-go
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway

# proto-vendor — подтягивает корелибовскую инфра-proto в proto/ перед buf-резолвом.
# Универсальная инфра (operation/validation/authz_options/cloud-api + google/*)
# принадлежит kacho-corelib (единственный источник) и в git здесь НЕ хранится
# (см. .gitignore). Файлы нужны только для buf-резолва импортов доменного proto;
# их Go-stubs генерируются и живут в kacho-corelib / canonical genproto, поэтому
# kacho-vpc их не дублирует и не генерирует (см. proto/buf.gen.yaml inputs.paths).
CORELIB_PROTO  ?= ../kacho-corelib/proto
VENDORED_PROTO := \
	google/api/annotations.proto \
	google/api/field_behavior.proto \
	google/api/http.proto \
	google/rpc/status.proto \
	kacho/cloud/api/operation.proto \
	kacho/cloud/operation/operation.proto \
	kacho/cloud/validation.proto \
	kacho/iam/authz/v1/authz_options.proto

proto-vendor:
	@for f in $(VENDORED_PROTO); do \
		mkdir -p proto/$$(dirname $$f); \
		cp $(CORELIB_PROTO)/$$f proto/$$f; \
	done

proto-lint: proto-vendor
	cd proto && buf lint

# proto-gen — регенерация Go-stubs доменного proto vpc (kacho/cloud/vpc/v1 +
# kacho/cloud/reference) из proto/. Зависит от proto-vendor, который подтягивает
# корелибовскую инфра-proto (operation/validation/authz_options/cloud-api/google)
# для buf-резолва импортов; сама инфра НЕ генерируется (Go-stubs живут в
# kacho-corelib / canonical genproto) — см. proto/buf.gen.yaml inputs.paths.
proto-gen: proto-vendor
	cd proto && buf generate

.PHONY: migrate-up migrate-down migrate-status
# migrate-* дергают отдельный binary `bin/kacho-migrator`.
# Зависимость на build-migrator гарантирует, что bin/ актуальный.
migrate-up: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) up

migrate-down: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) down

migrate-status: build-migrator
	KACHO_VPC_DB_PASSWORD=secret bin/$(MIGRATOR_BIN) status
