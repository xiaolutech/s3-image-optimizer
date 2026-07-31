default:
    @just --list

fmt:
    gofmt -w cmd internal

test:
    go test ./...

vet:
    go vet ./...

build:
    go build -o s3-image-sidecar ./cmd/s3-image-sidecar

validate: fmt test vet build
