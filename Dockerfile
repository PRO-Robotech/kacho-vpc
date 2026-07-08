# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

FROM --platform=$BUILDPLATFORM mirror.gcr.io/library/golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Single-repo build: зависимости (kacho-corelib, kacho-iam, kacho-geo) тянутся
# как versioned-модули из GitHub (go.mod без replace), build-context — этот репо.
COPY . .
RUN go mod download
# Два независимых binary в одном образе:
# kacho-vpc — gRPC API-сервер (только `serve`).
# kacho-migrator — CLI миграций (cobra: up|down|status|create), используется
# init-container'ом перед стартом основного pod'а.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /kacho-vpc ./cmd/vpc \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /kacho-migrator ./cmd/migrator

FROM mirror.gcr.io/library/alpine:3.20
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates
COPY --from=builder /kacho-vpc /usr/local/bin/kacho-vpc
COPY --from=builder /kacho-migrator /usr/local/bin/kacho-migrator
USER 65532
ENTRYPOINT ["/usr/local/bin/kacho-vpc"]
