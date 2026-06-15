package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"venturo-skeleton-go/pkg/logger"
)

// S3Client implements Client against any S3-compatible backend (AWS S3 or
// MinIO). For MinIO, set Endpoint + UsePathStyle. The AWS SDK v2 client is
// stateless, so Close is a no-op.
type S3Client struct {
	client    *s3.Client
	presign   *s3.PresignClient
	uploader  *manager.Uploader
	bucket    string
	publicURL string // optional base URL for PublicURL
	endpoint  string
	pathStyle bool
}

// NewS3Client builds an S3/MinIO-backed storage client from cfg.
func NewS3Client(ctx context.Context, cfg Config) (*S3Client, error) {
	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("storage(s3): S3_BUCKET is required")
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.S3Region),
	}
	if cfg.S3AccessKeyID != "" && cfg.S3SecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("storage(s3): load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
		}
		o.UsePathStyle = cfg.S3UsePathStyle
	})

	logger.Info("S3 storage client initialized",
		logger.String("provider", cfg.Provider),
		logger.String("bucket", cfg.S3Bucket),
		logger.String("endpoint", cfg.S3Endpoint),
		logger.Bool("path_style", cfg.S3UsePathStyle),
	)

	return &S3Client{
		client:    client,
		presign:   s3.NewPresignClient(client),
		uploader:  manager.NewUploader(client),
		bucket:    cfg.S3Bucket,
		publicURL: strings.TrimRight(cfg.S3PublicBaseURL, "/"),
		endpoint:  strings.TrimRight(cfg.S3Endpoint, "/"),
		pathStyle: cfg.S3UsePathStyle,
	}, nil
}

func (s *S3Client) Upload(ctx context.Context, input *UploadInput) (*FileInfo, error) {
	out, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(input.ObjectPath),
		Body:        input.Reader,
		ContentType: aws.String(input.ContentType),
	})
	if err != nil {
		return nil, fmt.Errorf("storage(s3): upload %q: %w", input.ObjectPath, err)
	}
	_ = out

	return &FileInfo{
		ObjectPath: input.ObjectPath,
		PublicURL:  s.PublicURL(input.ObjectPath),
		// The high-level uploader doesn't return size; callers that need it
		// can stat. Left zero to avoid an extra round-trip.
		Size: 0,
	}, nil
}

func (s *S3Client) Delete(ctx context.Context, objectPath string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectPath),
	})
	if err != nil {
		return fmt.Errorf("storage(s3): delete %q: %w", objectPath, err)
	}
	return nil
}

func (s *S3Client) SignedURL(ctx context.Context, objectPath string, expiry time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectPath),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("storage(s3): presign %q: %w", objectPath, err)
	}
	return req.URL, nil
}

func (s *S3Client) PublicURL(objectPath string) string {
	if s.publicURL != "" {
		return fmt.Sprintf("%s/%s", s.publicURL, objectPath)
	}
	if s.endpoint != "" {
		if s.pathStyle {
			return fmt.Sprintf("%s/%s/%s", s.endpoint, s.bucket, objectPath)
		}
		return fmt.Sprintf("%s/%s", s.endpoint, objectPath)
	}
	// AWS virtual-hosted style fallback.
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", s.bucket, objectPath)
}

// Close is a no-op: the AWS SDK v2 client holds no long-lived resources.
func (s *S3Client) Close() error { return nil }
