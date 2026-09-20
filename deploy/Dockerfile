# syntax=docker/dockerfile:1.7
# =============================================================================
# XIASS API Multi-Stage Dockerfile
# =============================================================================
# Stage 1: Build frontend
# Stage 2: Build Go backend with embedded frontend
# Stage 3: Subscription proxy sidecar
# Stage 4: Final minimal image
# =============================================================================

ARG NODE_IMAGE=node:24-alpine
ARG GOLANG_IMAGE=golang:1.27.0-alpine
ARG ALPINE_IMAGE=alpine:3.20
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

# -----------------------------------------------------------------------------
# Stage 1: Frontend Builder
# -----------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS frontend-builder

WORKDIR /app/frontend
# Vite's production bundle includes the complete admin surface and can exceed
# Node's default heap in small build VMs. Keep the limit explicit and
# overridable so local 4 GiB builders and CI do not fail nondeterministically.
ARG NODE_MAX_OLD_SPACE_SIZE=3072
ENV NODE_OPTIONS=--max-old-space-size=${NODE_MAX_OLD_SPACE_SIZE}

# Install pnpm. Keep this pinned to the lockfile-compatible major version so
# Docker builds remain reproducible when pnpm changes config validation rules.
RUN corepack enable && corepack prepare pnpm@10.15.0 --activate

# Install dependencies first (better caching)
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml frontend/.npmrc ./
RUN pnpm install --frozen-lockfile

# Copy frontend source and build.
# LegalDocumentView.vue (admin-compliance gate) build-time imports
# ../../../../docs/legal/*.md?raw, so docs/legal/ must sit beside frontend/
# in the image (WORKDIR /app/frontend -> resolves to /app/docs/legal/*.md).
# Copy only that subtree to keep the build dependency minimal.
COPY frontend/ ./
COPY docs/legal/ /app/docs/legal/
RUN pnpm run build

# -----------------------------------------------------------------------------
# Stage 2: Backend Builder
# -----------------------------------------------------------------------------
FROM ${GOLANG_IMAGE} AS backend-builder

# Build arguments for version info (set by CI)
ARG VERSION=
ARG COMMIT=docker
ARG DATE
ARG GOPROXY
ARG GOSUMDB

ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app/backend

# Copy go mod files first (better caching)
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy backend source first
COPY backend/ ./

# Copy frontend dist from previous stage (must be after backend copy to avoid being overwritten)
COPY --from=frontend-builder /app/backend/internal/web/dist ./internal/web/dist

# Build the binary (BuildType=release for CI builds, embed frontend)
# Version precedence: build arg VERSION > exact git tag > cmd/server/VERSION
RUN VERSION_VALUE="${VERSION}" && \
    if [ -z "${VERSION_VALUE}" ]; then VERSION_VALUE="$(./scripts/resolve-version.sh)"; fi && \
    DATE_VALUE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=linux go build \
    -tags embed \
    -ldflags="-s -w -X main.Version=${VERSION_VALUE} -X main.Commit=${COMMIT} -X main.Date=${DATE_VALUE} -X main.BuildType=release" \
    -o /app/xiass-api \
    ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: Subscription proxy sidecar
# -----------------------------------------------------------------------------
FROM ${GOLANG_IMAGE} AS proxy-agent-builder

ARG GOPROXY
ARG GOSUMDB

ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

WORKDIR /app/proxy-agent
COPY tools/xiass-proxy-agent/go.mod tools/xiass-proxy-agent/go.sum ./
COPY tools/xiass-proxy-agent/third_party/ccodex-sleep-state/go.mod tools/xiass-proxy-agent/third_party/ccodex-sleep-state/go.sum ./third_party/ccodex-sleep-state/
RUN go mod download
COPY tools/xiass-proxy-agent/ ./
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" -trimpath \
    -o /app/xiass-proxy-agent \
    ./cmd/xiass-proxy-agent

# -----------------------------------------------------------------------------
# Stage 4: Final Runtime Image
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE}

# Labels
LABEL maintainer="XIASS API <github.com/xyf0104/xiass-api>"
LABEL description="XIASS API - AI API Gateway Platform"
LABEL org.opencontainers.image.source="https://github.com/xyf0104/xiass-api"

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    curl \
    su-exec \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN addgroup -g 1000 xiass && \
    adduser -u 1000 -G xiass -s /bin/sh -D xiass

# Set working directory
WORKDIR /app

# Copy the XIASS binary and runtime fallback pricing resources from the builder.
COPY --from=backend-builder --chown=xiass:xiass /app/xiass-api /app/xiass-api
COPY --from=backend-builder --chown=xiass:xiass /app/backend/resources /app/resources
COPY --from=proxy-agent-builder --chown=xiass:xiass /app/xiass-proxy-agent /app/xiass-proxy-agent
COPY --chown=xiass:xiass tools/xiass-proxy-agent/LICENSE /app/licenses/xiass-proxy-agent/LICENSE
COPY --chown=xiass:xiass tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json /app/licenses/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json
# .dockerignore excludes Markdown globally. Keep this generated copy identical
# to tools/xiass-proxy-agent/THIRD_PARTY_NOTICES.md for source-built images.
COPY --chown=xiass:xiass <<'EOF' /app/licenses/xiass-proxy-agent/THIRD_PARTY_NOTICES.md
# Third-Party Notices

This component is distributed under GPL-3.0-only.

## luweiming1/ccodex-sleep-state

Vendored from commit `89294a1c723c7f061fa60afa42710e17a40ec6a6` under
GPL-3.0-only. Exact original copies of `internal/proxyroute/parse.go`,
`internal/proxyroute/route.go`, and `internal/settings/settings.go` are retained
under `third_party/ccodex-sleep-state/upstream-original/`. The build copies add
canonical node/source attribution metadata and an explicit per-subscription
TLS opt-in. This optional setting defaults to false, permits only supplied
node certificate-verification exceptions, and retains subscription-download
TLS verification and forbidden local-file/routing validation. Original parsing,
loading, filtering, stable identity and adapter construction remain in use.
These XIASS adaptations were updated on 2026-09-20. See
`UPSTREAM_SOURCE_MANIFEST.json` for the original and adapted source hashes.

The upstream GPL text is retained at
`third_party/ccodex-sleep-state/LICENSE`; the component-level GPL text is in
`LICENSE`.

## github.com/metacubex/mihomo v1.19.31

Used by the upstream code for proxy URI conversion and outbound protocol
adapters. License: GPL-3.0-only.

## gopkg.in/yaml.v3 v3.0.1

Used by the upstream parser for Clash/Mihomo YAML. License: MIT.

The upstream module's complete dependency versions are preserved in
`third_party/ccodex-sleep-state/go.mod` and `go.sum`. The root module resolves
that local module through a Go `replace` directive.

Distribution of a binary containing this code must satisfy GPL-3.0 source,
license, notice, and modification-marking obligations. Deployment as a
separate local process preserves a clear operational boundary, but does not
remove those distribution obligations.
EOF
RUN ln -s /app/xiass-api /app/nowind-api && ln -s /app/xiass-api /app/sub2api

# Create data directory
RUN mkdir -p /app/data && chown -R xiass:xiass /app

# Copy entrypoint script (fixes volume permissions then drops to UID/GID 1000)
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# Expose port (can be overridden by SERVER_PORT env var)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:${SERVER_PORT:-8080}/health || exit 1

# Run the canonical XIASS API executable.
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/xiass-api"]
