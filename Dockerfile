FROM golang:1.25-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -tags nodynamic -a -installsuffix cgo -o s3-image-optimizer ./cmd/s3-image-optimizer

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/s3-image-optimizer .
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/app/s3-image-optimizer", "healthcheck"]
CMD ["/app/s3-image-optimizer"]
