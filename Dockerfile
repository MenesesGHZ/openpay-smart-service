# ─── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

# Install protoc plugins and buf CLI for proto code generation.
# Pinned to versions that match go.mod and the existing generated files.
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0 \
    && go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.20.0 \
    && go install github.com/bufbuild/buf/cmd/buf@v1.32.2 \
    && go install github.com/pressly/goose/v3/cmd/goose@latest

WORKDIR /app

# ── Proto generation (cached until proto/* or buf config changes) ─────────────
# buf.lock pins the googleapis dependency digest so no network fetch is needed.
COPY buf.yaml buf.lock buf.gen.yaml ./
COPY proto ./proto
RUN buf generate

# ── Go dependencies (cached until go.mod/go.sum change) ──────────────────────
COPY go.mod go.sum ./
RUN go mod download

# ── Application source ────────────────────────────────────────────────────────
# gen/ is in .dockerignore so the freshly generated stubs above are used.
COPY . .

# ── Compile ───────────────────────────────────────────────────────────────────
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /bin/server ./cmd/server

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /bin/worker ./cmd/worker

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
