# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy remaining source
COPY . .

# Build static binary with config path set at build time
ARG CONFIG_PATH=/etc/prompt-sudo-discord/config.json
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static' -X main.configPath=${CONFIG_PATH}" \
    -o prompt-sudo-discord \
    .

# Final minimal image to extract binary
FROM scratch
COPY --from=builder /build/prompt-sudo-discord /prompt-sudo-discord
CMD ["/prompt-sudo-discord"]
