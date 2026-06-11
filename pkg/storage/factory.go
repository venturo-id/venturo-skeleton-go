package storage

import (
	"context"
	"fmt"
	"strings"
)

// New builds a storage Client for the configured provider. Defaults to GCS for
// backward compatibility. The returned Client satisfies the existing
// storage.Client interface, so consumers are provider-agnostic.
func New(ctx context.Context, cfg Config) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "gcs":
		return NewGCSClient(ctx, cfg.GCSBucket, cfg.GCSCredentialsJSON)
	case "s3", "minio":
		return NewS3Client(ctx, cfg)
	default:
		return nil, fmt.Errorf("storage: unknown provider %q (want gcs|s3|minio)", cfg.Provider)
	}
}
