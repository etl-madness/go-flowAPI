# syntax=docker/dockerfile:1

# Stage 0: Placeholder stage overridden by `additional_contexts: go-flow: ../go-flow`
# in docker-compose*.yml (or `--build-context go-flow=../go-flow` with `docker build`).
FROM scratch AS go-flow

# Stage 1: Build go-flowAPI binary
FROM golang:1.26-alpine AS builder

ENV GOTOOLCHAIN=auto
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy local ../go-flow module required by go.mod replace directive
COPY --from=go-flow . /src/go-flow/

# Copy go-flowAPI module files and download dependencies
WORKDIR /src/go-flowAPI
COPY go.mod go.sum ./
RUN go mod download

# Copy go-flowAPI source and compile static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/go-flowAPI .

# Stage 2: Minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata sqlite wget \
    && mkdir -p /app /data

WORKDIR /app

COPY --from=builder /out/go-flowAPI /app/go-flowAPI
COPY docker/init/ /app/docker/init/
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Normalize line endings in case of Windows CRLF checkout and make executable
RUN sed -i 's/\r$//' /usr/local/bin/docker-entrypoint.sh \
    && chmod +x /usr/local/bin/docker-entrypoint.sh /app/go-flowAPI

ENV PORT=8080 \
    FLOW_API_ADDR=:8080

EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
