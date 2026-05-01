# ─── Build stage ────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

# Install git (needed for go get with private modules) and ca-certs.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy module files first for layer-cached dependency downloads.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY . .

# Build the orchestrator binary.
# CGO_ENABLED=0 produces a fully static binary that runs in a scratch image.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /orchestrator ./cmd/orchestrator

# ─── Runtime stage ───────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the static binary and CA certificates from the builder.
COPY --from=builder /orchestrator /orchestrator
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8080

ENTRYPOINT ["/orchestrator"]
