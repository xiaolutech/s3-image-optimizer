package storage

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/xiaolutech/s3-image-optimizer/internal/config"
)

func TestS3ClientObjectOperations(t *testing.T) {
	ctx := context.Background()
	endpoint := startMinIO(t, ctx)
	sourceBucket := "source-assets"
	optimizedBucket := "source-assets-optimized"

	rawClient := newRawS3Client(t, ctx, endpoint)
	createBucket(t, ctx, rawClient, sourceBucket)
	createBucket(t, ctx, rawClient, optimizedBucket)
	putRawObject(t, ctx, rawClient, sourceBucket, "notes/photo.jpg", []byte("source image"), "image/jpeg", map[string]string{
		"Source-Case": "Mixed",
	})

	client, err := New(testConfig(endpoint, sourceBucket, optimizedBucket))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	var listed []string
	err = client.ListObjects(ctx, sourceBucket, "notes/", func(info ObjectInfo) error {
		listed = append(listed, info.Key)
		return nil
	})
	if err != nil {
		t.Fatalf("ListObjects failed: %v", err)
	}
	if len(listed) != 1 || listed[0] != "notes/photo.jpg" {
		t.Fatalf("unexpected listed keys: %#v", listed)
	}

	info, err := client.HeadObject(ctx, sourceBucket, "notes/photo.jpg")
	if err != nil {
		t.Fatalf("HeadObject failed: %v", err)
	}
	if info.Key != "notes/photo.jpg" {
		t.Fatalf("unexpected key %q", info.Key)
	}
	if info.Size != int64(len("source image")) {
		t.Fatalf("unexpected size %d", info.Size)
	}
	if info.ETag == "" {
		t.Fatal("expected ETag")
	}
	if info.ContentType != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %q", info.ContentType)
	}
	if info.Metadata["source-case"] != "Mixed" {
		t.Fatalf("expected lower-case metadata key, got %#v", info.Metadata)
	}

	body, gotInfo, err := client.GetObject(ctx, sourceBucket, "notes/photo.jpg")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if string(body) != "source image" {
		t.Fatalf("unexpected body %q", string(body))
	}
	if gotInfo.ETag != info.ETag {
		t.Fatalf("expected matching ETag, got %q and %q", gotInfo.ETag, info.ETag)
	}

	err = client.PutObject(ctx, optimizedBucket, "notes/photo.jpg", []byte("optimized image"), PutOptions{
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"source-etag":          info.ETag,
			"optimization-profile": "v2-jpeg82-png-best-original-width",
		},
	})
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	optimizedInfo, err := client.HeadObject(ctx, optimizedBucket, "notes/photo.jpg")
	if err != nil {
		t.Fatalf("Head optimized failed: %v", err)
	}
	if optimizedInfo.Metadata["source-etag"] != info.ETag {
		t.Fatalf("expected source-etag metadata, got %#v", optimizedInfo.Metadata)
	}
	if optimizedInfo.Metadata["optimization-profile"] != "v2-jpeg82-png-best-original-width" {
		t.Fatalf("expected optimization-profile metadata, got %#v", optimizedInfo.Metadata)
	}
}

func TestS3ClientNotFound(t *testing.T) {
	ctx := context.Background()
	endpoint := startMinIO(t, ctx)
	sourceBucket := "source-assets"

	rawClient := newRawS3Client(t, ctx, endpoint)
	createBucket(t, ctx, rawClient, sourceBucket)

	client, err := New(testConfig(endpoint, sourceBucket, "optimized-assets"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	_, err = client.HeadObject(ctx, sourceBucket, "missing.jpg")
	if err == nil {
		t.Fatal("expected missing object error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound true for %T %v", err, err)
	}
}

func TestS3ClientListStopsOnVisitError(t *testing.T) {
	ctx := context.Background()
	endpoint := startMinIO(t, ctx)
	sourceBucket := "source-assets"

	rawClient := newRawS3Client(t, ctx, endpoint)
	createBucket(t, ctx, rawClient, sourceBucket)
	putRawObject(t, ctx, rawClient, sourceBucket, "one.jpg", []byte("one"), "image/jpeg", nil)

	client, err := New(testConfig(endpoint, sourceBucket, "optimized-assets"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	wantErr := errors.New("stop")
	err = client.ListObjects(ctx, sourceBucket, "", func(info ObjectInfo) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected visit error, got %v", err)
	}
}

func startMinIO(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "minio/minio:RELEASE.2024-01-16T16-07-38Z",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ACCESS_KEY": "minioadmin",
			"MINIO_SECRET_KEY": "minioadmin",
		},
		Cmd:        []string{"server", "/data"},
		WaitingFor: wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate minio: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("minio host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000")
	if err != nil {
		t.Fatalf("minio port: %v", err)
	}
	return host + ":" + port.Port()
}

func newRawS3Client(t *testing.T, ctx context.Context, endpoint string) *awss3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String("http://" + endpoint)
	})
}

func createBucket(t *testing.T, ctx context.Context, client *awss3.Client, bucket string) {
	t.Helper()
	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create bucket %s: %v", bucket, err)
	}
}

func putRawObject(t *testing.T, ctx context.Context, client *awss3.Client, bucket, key string, body []byte, contentType string, metadata map[string]string) {
	t.Helper()
	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatalf("put object %s/%s: %v", bucket, key, err)
	}
}

func testConfig(endpoint, sourceBucket, optimizedBucket string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.S3Endpoint = strings.TrimPrefix(endpoint, "http://")
	cfg.S3AccessKeyID = "minioadmin"
	cfg.S3SecretAccessKey = "minioadmin"
	cfg.S3UseSSL = false
	cfg.SourceBucket = sourceBucket
	cfg.OptimizedBucket = optimizedBucket
	return cfg
}
