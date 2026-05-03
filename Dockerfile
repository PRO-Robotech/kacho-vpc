FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY kacho-corelib /src/kacho-corelib
COPY kacho-proto /src/kacho-proto
COPY kacho-vpc /src/kacho-vpc

WORKDIR /src/kacho-vpc
RUN go mod download
RUN CGO_ENABLED=0 go build -o /kacho-vpc ./cmd/kacho-vpc

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /kacho-vpc /usr/local/bin/kacho-vpc
USER 65532
ENTRYPOINT ["/usr/local/bin/kacho-vpc"]
