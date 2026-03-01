# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and data
COPY . .

# Build binary
RUN CGO_ENABLED=0 go build -o /api ./cmd/api

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

# Copy binary and data
COPY --from=builder /api .
COPY --from=builder /build/data ./data

# Defaults; override via docker-compose or -e
ENV PORT=8080

EXPOSE 8080

CMD ["./api"]
