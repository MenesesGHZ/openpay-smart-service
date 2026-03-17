package storage

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/menesesghz/openpay-smart-service/internal/config"
)

// Storage is the interface for uploading tenant assets.
type Storage interface {
	// UploadLogo stores logo bytes in S3 and returns the public URL.
	// key is the object key, e.g. "tenant-logos/<tenantID>/<filename>".
	UploadLogo(ctx context.Context, key, contentType string, data []byte) (string, error)

	// DeleteObject removes an object by key. Used when a logo is replaced.
	DeleteObject(ctx context.Context, key string) error
}

// S3Storage implements Storage backed by AWS S3 (or LocalStack / MinIO).
type S3Storage struct {
	client    *s3.Client
	bucket    string
	publicURL string // base URL for constructing public file URLs
}

// NewS3Storage builds an S3Storage from the service config.
// When cfg.Endpoint is set, requests are routed there (LocalStack / MinIO).
// When cfg.AccessKeyID is empty the SDK falls back to its default credential
// chain (IAM role, env vars, ~/.aws/credentials).
func NewS3Storage(ctx context.Context, cfg config.S3Config) (*S3Storage, error) {
	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(cfg.Region),
	}

	// Static credentials — used for LocalStack in dev; in prod use IAM roles.
	if cfg.AccessKeyID != "" {
		opts = append(opts, awscfg.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, ""),
		))
	}

	awsCfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	clientOpts := []func(*s3.Options){}
	if cfg.Endpoint != "" {
		// Force path-style addressing required by LocalStack.
		clientOpts = append(clientOpts,
			func(o *s3.Options) {
				o.BaseEndpoint = aws.String(cfg.Endpoint)
				o.UsePathStyle = true
			},
		)
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)

	return &S3Storage{
		client:    client,
		bucket:    cfg.Bucket,
		publicURL: strings.TrimRight(cfg.PublicURL, "/"),
	}, nil
}

// UploadLogo puts logo bytes into S3 and returns the public-accessible URL.
func (s *S3Storage) UploadLogo(ctx context.Context, key, contentType string, data []byte) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
		// Objects are public-readable (static assets pattern).
		// In production, serve via CloudFront instead of direct S3 URLs.
		ACL: "public-read",
	})
	if err != nil {
		return "", fmt.Errorf("s3 put object %q: %w", key, err)
	}

	url := fmt.Sprintf("%s/%s", s.publicURL, key)
	return url, nil
}

// DeleteObject removes an object from S3. Errors are swallowed if the object
// does not exist (idempotent delete).
func (s *S3Storage) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 delete object %q: %w", key, err)
	}
	return nil
}
