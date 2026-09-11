# Build the web and Go stages on the build platform and cross-compile, so a
# multi-architecture release never runs Go or npm under QEMU emulation.
# Emulated `go mod download` failed the v2.5.1 release build.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web-builder
WORKDIR /build/web
ENV npm_config_registry=https://registry.npmmirror.com
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
WORKDIR /build
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ENV GOSUMDB=sum.golang.google.cn
ARG VERSION=dev
ARG COMMIT=unknown
# Filled in by buildx per target platform; empty means the build platform.
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /build/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X github.com/lan/meta-gateway/internal/buildinfo.Version=${VERSION} -X github.com/lan/meta-gateway/internal/buildinfo.Commit=${COMMIT}" \
    -o /bin/meta-gateway ./cmd/server

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
