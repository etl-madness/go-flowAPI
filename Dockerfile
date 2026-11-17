# syntax=docker/dockerfile:1

# Stage 1: Build go-flowAPI binary
FROM golang:1.26-alpine AS builder

ENV GOTOOLCHAIN=auto
RUN apk add --no-cache git ca-certificates

WORKDIR /src/go-flowAPI
COPY go.mod go.sum ./
RUN go mod download

# Copy go-flowAPI source and compile static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/go-flowAPI .

# Stage 2: Runtime image with .NET SDK and PowerShell
FROM mcr.microsoft.com/dotnet/sdk:10.0-alpine3.24

RUN apk add --no-cache ca-certificates tzdata sqlite wget krb5 krb5-libs \
    && mkdir -p /app /data /krb5cache

RUN dotnet tool install --global dotnet-script
ENV PATH="$PATH:/root/.dotnet/tools"

WORKDIR /app

COPY --from=builder /out/go-flowAPI /app/go-flowAPI
COPY docker/init/ /app/docker/init/
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Normalize line endings in case of Windows CRLF checkout and make executable
RUN sed -i 's/\r$//' /usr/local/bin/docker-entrypoint.sh \
    && chmod +x /usr/local/bin/docker-entrypoint.sh /app/go-flowAPI

ENV PORT=5180 \
    FLOW_API_ADDR=:5180

EXPOSE 5180
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
