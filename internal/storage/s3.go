package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/xiaolutech/s3-image-optimizer/internal/config"
)

type ObjectInfo struct {
	Key         string
	Size        int64
	ETag        string
	ContentType string
	Metadata    map[string]string
}

type ListPage struct {
	Objects []ObjectInfo
	HasMore bool
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

type Client struct {
	client *s3.Client
}

func New(cfg *config.Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKeyID,
			cfg.S3SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	endpoint := normalizeEndpoint(cfg.S3Endpoint, cfg.S3UseSSL)
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
		o.DisableLogOutputChecksumValidationSkipped = true
	})
	return &Client{client: client}, nil
}

func (c *Client) HeadObject(ctx context.Context, bucket, key string) (*ObjectInfo, error) {
	out, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return objectInfoFromHead(key, out), nil
}

func (c *Client) GetObject(ctx context.Context, bucket, key string) ([]byte, *ObjectInfo, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, err
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read object body: %w", err)
	}
	return body, objectInfoFromGet(key, out), nil
}

func (c *Client) PutObject(ctx context.Context, bucket, key string, body []byte, opts PutOptions) error {
	input := &s3.PutObjectInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		Body:     bytes.NewReader(body),
		Metadata: opts.Metadata,
	}
	if opts.ContentType != "" {
		input.ContentType = aws.String(opts.ContentType)
	}
	if _, err := c.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("put object %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *Client) ListObjects(ctx context.Context, bucket, prefix string, visit func(ObjectInfo) error) error {
	paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		for _, object := range page.Contents {
			info := ObjectInfo{
				Key:  aws.ToString(object.Key),
				Size: aws.ToInt64(object.Size),
				ETag: trimETag(aws.ToString(object.ETag)),
			}
			if err := visit(info); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) ListObjectsPage(ctx context.Context, bucket, prefix, startAfter string, maxKeys int32) (ListPage, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(maxKeys),
	}
	if startAfter != "" {
		input.StartAfter = aws.String(startAfter)
	}
	out, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return ListPage{}, fmt.Errorf("list objects page: %w", err)
	}
	page := ListPage{
		Objects: make([]ObjectInfo, 0, len(out.Contents)),
		HasMore: aws.ToBool(out.IsTruncated),
	}
	for _, object := range out.Contents {
		page.Objects = append(page.Objects, ObjectInfo{
			Key:  aws.ToString(object.Key),
			Size: aws.ToInt64(object.Size),
			ETag: trimETag(aws.ToString(object.ETag)),
		})
	}
	return page, nil
}

func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "NoSuchBucket":
			return true
		}
	}
	return false
}

func normalizeEndpoint(endpoint string, useSSL bool) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	if useSSL {
		return "https://" + endpoint
	}
	return "http://" + endpoint
}

func objectInfoFromHead(key string, out *s3.HeadObjectOutput) *ObjectInfo {
	return &ObjectInfo{
		Key:         key,
		Size:        aws.ToInt64(out.ContentLength),
		ETag:        trimETag(aws.ToString(out.ETag)),
		ContentType: aws.ToString(out.ContentType),
		Metadata:    normalizeMetadata(out.Metadata),
	}
}

func objectInfoFromGet(key string, out *s3.GetObjectOutput) *ObjectInfo {
	return &ObjectInfo{
		Key:         key,
		Size:        aws.ToInt64(out.ContentLength),
		ETag:        trimETag(aws.ToString(out.ETag)),
		ContentType: aws.ToString(out.ContentType),
		Metadata:    normalizeMetadata(out.Metadata),
	}
}

func normalizeMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	result := make(map[string]string, len(metadata))
	for key, value := range metadata {
		result[strings.ToLower(key)] = value
	}
	return result
}

func trimETag(etag string) string {
	return strings.Trim(etag, `"`)
}
