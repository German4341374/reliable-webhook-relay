# syntax=docker/dockerfile:1.10
FROM golang:1.26.6-alpine3.24@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/relay ./cmd/relay && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/demo-receiver ./cmd/demo-receiver && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck && \
    mkdir -p /out/data && touch /out/data/.keep

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

WORKDIR /app
COPY --from=build --chown=65532:65532 /out/relay /relay
COPY --from=build --chown=65532:65532 /out/demo-receiver /demo-receiver
COPY --from=build --chown=65532:65532 /out/healthcheck /healthcheck
COPY --from=build --chown=65532:65532 /out/data /data
COPY --chown=65532:65532 config/config.example.json /app/config/config.example.json

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/relay"]
CMD ["-config", "/app/config/config.example.json"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/healthcheck", "http://127.0.0.1:8080/health"]
