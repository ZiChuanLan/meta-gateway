FROM node:24-alpine AS web-builder
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /build/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/meta-gateway ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl \
    && addgroup -S -g 10001 metagateway \
    && adduser -S -D -H -u 10001 -G metagateway metagateway \
    && install -d -o metagateway -g metagateway -m 0700 /data /data/backups
COPY --from=builder /bin/meta-gateway /usr/local/bin/meta-gateway
USER metagateway:metagateway
WORKDIR /data
EXPOSE 4100
ENTRYPOINT ["meta-gateway"]
