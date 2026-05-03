BINARY := kacho-vpc
CMD     := ./cmd/vpc
IMAGE   := kacho-vpc:dev

.PHONY: build test vet lint docker sync-migrations generate

build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) $(CMD)

test:
	go test ./... -race -cover -timeout 300s

test-short:
	go test ./... -race -cover -short -timeout 120s

vet:
	go vet ./...

lint:
	golangci-lint run ./... || true

sync-migrations:
	cp ../kacho-corelib/migrations/common/*.sql migrations/
	cp ../kacho-corelib/migrations/common/*.sql internal/migrations/

docker:
	cd .. && docker build -f kacho-vpc/Dockerfile -t $(IMAGE) .

.PHONY: migrate-up migrate-down migrate-status
migrate-up:
	KACHO_VPC_DB_PASSWORD=secret bin/$(BINARY) migrate up

migrate-down:
	KACHO_VPC_DB_PASSWORD=secret bin/$(BINARY) migrate down

migrate-status:
	KACHO_VPC_DB_PASSWORD=secret bin/$(BINARY) migrate status
