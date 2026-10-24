# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS development
RUN apk add --no-cache ca-certificates git
WORKDIR /app
ENV GOTOOLCHAIN=local

FROM development AS build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/devcheck ./cmd/devcheck

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 cycrio && adduser -S -D -u 10001 -G cycrio cycrio
WORKDIR /app
COPY --from=build /out/ /app/
COPY deploy/docker/runtime-entrypoint.sh /app/entrypoint.sh
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/bin/sh", "/app/entrypoint.sh"]
CMD ["api"]
