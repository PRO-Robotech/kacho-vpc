FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY kacho-corelib /src/kacho-corelib
COPY kacho-proto /src/kacho-proto
COPY kacho-vpc /src/kacho-vpc

WORKDIR /src/kacho-vpc
RUN go mod download
# KAC-96 (skill evgeniy §9 K.1, AP-9): два независимых binary в одном образе.
# kacho-vpc — gRPC API-сервер (только `serve`).
# kacho-migrator — CLI миграций (cobra: up|down|status|create), используется
# init-container'ом перед стартом основного pod'а.
RUN CGO_ENABLED=0 go build -o /kacho-vpc ./cmd/vpc \
 && CGO_ENABLED=0 go build -o /kacho-migrator ./cmd/migrator

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /kacho-vpc /usr/local/bin/kacho-vpc
COPY --from=builder /kacho-migrator /usr/local/bin/kacho-migrator
USER 65532
ENTRYPOINT ["/usr/local/bin/kacho-vpc"]
