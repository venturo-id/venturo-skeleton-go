package storage

// Config selects and configures a storage backend. It is defined locally in
// pkg/storage (not internal/config) so this package never imports
// internal/config — consistent with NewGCSClient taking discrete args and with
// pkg/email.SMTPConfig. main.go maps internal/config values into this struct.
type Config struct {
	Provider string // gcs | s3 | minio

	// GCS settings.
	GCSBucket          string
	GCSCredentialsJSON string

	// S3 / MinIO settings. MinIO is S3-compatible: set S3Endpoint +
	// S3UsePathStyle=true.
	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3PublicBaseURL   string
	S3UsePathStyle    bool
}
