# syntax=docker/dockerfile:1.8
FROM golang:1.25-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -tags 'nodynamic netgo osusergo' \
      -ldflags '-linkmode external -extldflags "-static"' \
      -o s3-image-optimizer ./cmd/s3-image-optimizer

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/s3-image-optimizer .
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/app/s3-image-optimizer", "healthcheck"]
CMD ["/app/s3-image-optimizer"]
