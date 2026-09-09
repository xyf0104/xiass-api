ARG GOLANG_IMAGE=golang:1.26.6-alpine
ARG ALPINE_IMAGE=alpine:3.20

FROM ${GOLANG_IMAGE} AS builder
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/xiass-witness ./cmd/witness

FROM ${ALPINE_IMAGE}
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 xiass && \
    adduser -D -H -u 10001 -G xiass xiass && \
    mkdir -p /var/lib/xiass-witness && \
    chown -R xiass:xiass /var/lib/xiass-witness
COPY --from=builder --chown=xiass:xiass /out/xiass-witness /usr/local/bin/xiass-witness
USER xiass
VOLUME ["/var/lib/xiass-witness"]
EXPOSE 8091
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8091/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/xiass-witness"]
