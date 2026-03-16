# ─── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build all binaries
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s -X main.version=$(git describe --tags --always)" \
    -o /bin/server ./cmd/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /bin/worker ./cmd/worker

# Install goose for migrations
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

# ─── Server image ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS server

COPY --from=builder /bin/server /server
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

EXPOSE 50051 8080 9090

ENTRYPOINT ["/server"]

# ─── Worker image ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS worker

COPY --from=builder /bin/worker /worker
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT ["/worker"]

# ─── Migration image ──────────────────────────────────────────────────────────
FROM alpine:3.20 AS migrate

COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY migrations /migrations

WORKDIR /migrations
ENTRYPOINT ["goose"]
CMD ["up"]
