FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/logviewer .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates su-exec wget \
    && addgroup -S -g 65532 logviewer \
    && adduser -S -D -H -u 65532 -G logviewer logviewer

WORKDIR /data
COPY --from=build /out/logviewer /app/logviewer
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /app/logviewer /usr/local/bin/entrypoint.sh

EXPOSE 8765
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8765/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
