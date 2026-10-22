package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"venturo-skeleton-go-simple/pkg/logger"
)

// GCSClient implements Client using Google Cloud Storage.
type GCSClient struct {
	client     *storage.Client
	bucketName string
}

// NewGCSClient creates a GCS-backed storage client.
// credentialsJSON may be empty when running on GCP with default credentials.
func NewGCSClient(ctx context.Context, bucketName, credentialsJSON string) (*GCSClient, error) {
	var opts []option.ClientOption
	if credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credentialsJSON)))
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("storage.NewClient: %w", err)
	}

	logger.Info("GCS storage client initialized",
		logger.String("bucket", bucketName))

	return &GCSClient{
		client:     client,
		bucketName: bucketName,
	}, nil
}

func (g *GCSClient) Upload(ctx context.Context, input *UploadInput) (*FileInfo, error) {
	obj := g.client.Bucket(g.bucketName).Object(input.ObjectPath)
	w := obj.NewWriter(ctx)
	w.ContentType = input.ContentType

	written, err := io.Copy(w, input.Reader)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("io.Copy: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("writer.Close: %w", err)
	}

	return &FileInfo{
		ObjectPath: input.ObjectPath,
		PublicURL:  g.PublicURL(input.ObjectPath),
		Size:       written,
	}, nil
}

func (g *GCSClient) Delete(ctx context.Context, objectPath string) error {
	obj := g.client.Bucket(g.bucketName).Object(objectPath)
	if err := obj.Delete(ctx); err != nil {
		if err == storage.ErrObjectNotExist {
			return nil // idempotent
		}
		return fmt.Errorf("object.Delete: %w", err)
	}
	return nil
}

func (g *GCSClient) SignedURL(_ context.Context, objectPath string, expiry time.Duration) (string, error) {
	url, err := g.client.Bucket(g.bucketName).SignedURL(objectPath, &storage.SignedURLOptions{
		Method:  "GET",
		Expires: time.Now().Add(expiry),
	})
	if err != nil {
		return "", fmt.Errorf("bucket.SignedURL: %w", err)
	}
	return url, nil
}

func (g *GCSClient) PublicURL(objectPath string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", g.bucketName, objectPath)
}

// Close releases resources held by the underlying GCS client.
func (g *GCSClient) Close() error {
	return g.client.Close()
}
