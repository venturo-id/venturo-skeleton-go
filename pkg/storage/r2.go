package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// maxPresignTTL is the longest validity SigV4 allows for a presigned URL.
const maxPresignTTL = 7 * 24 * time.Hour

// Config holds the Cloudflare R2 settings.
type Config struct {
	// AccountID is the Cloudflare account id. The S3 endpoint is
	// https://<AccountID>.r2.cloudflarestorage.com.
	AccountID string
	// AccessKeyID and SecretAccessKey are the S3 credentials of an R2 API token.
	AccessKeyID     string
	SecretAccessKey string
	// Bucket is the bucket every key lives in.
	Bucket string
	// PublicURL is the optional base URL the bucket is publicly served from
	// (a custom domain or the bucket's r2.dev URL). Leave it empty for a
	// private bucket and hand out PresignGet URLs instead.
	PublicURL string
}

// Enabled reports whether every required field is set, i.e. whether the
// service should construct an R2 client at all.
func (c Config) Enabled() bool {
	return c.AccountID != "" && c.AccessKeyID != "" && c.SecretAccessKey != "" && c.Bucket != ""
}

// validate names the first missing or malformed field. Values are never
// echoed back: the error ends up in logs.
func (c Config) validate() error {
	required := []struct{ name, value string }{
		{"AccountID", c.AccountID},
		{"AccessKeyID", c.AccessKeyID},
		{"SecretAccessKey", c.SecretAccessKey},
		{"Bucket", c.Bucket},
	}
	for _, f := range required {
		if f.value == "" {
			return fmt.Errorf("storage: R2 %s is required", f.name)
		}
	}

	// The account id becomes part of the endpoint host.
	for _, r := range c.AccountID {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return errors.New("storage: R2 AccountID must contain only letters and digits")
		}
	}

	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.RawQuery != "" || u.Fragment != "" {
			return errors.New("storage: R2 PublicURL must be an absolute http(s) URL without query or fragment")
		}
	}
	return nil
}

// R2 stores objects in a Cloudflare R2 bucket. It is safe for concurrent use.
type R2 struct {
	client    *s3.Client
	presign   *s3.PresignClient
	bucket    string
	publicURL string // Config.PublicURL without trailing "/"; "" when not served publicly
}

var _ Storage = (*R2)(nil)

// NewR2 builds an R2 client from cfg. It does not contact R2, so a wrong
// credential or bucket only shows up on the first request.
func NewR2(cfg Config) (*R2, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	client := s3.New(s3.Options{
		Region:       "auto", // R2 has no regions; "auto" is what it signs against
		BaseEndpoint: aws.String("https://" + cfg.AccountID + ".r2.cloudflarestorage.com"),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: true,
		// Send checksums only when an operation requires one. By default the
		// SDK wraps every PutObject body in aws-chunked encoding with a
		// trailing CRC32; a plain body is the shape every S3-compatible
		// store accepts.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})

	return &R2{
		client:    client,
		presign:   s3.NewPresignClient(client),
		bucket:    cfg.Bucket,
		publicURL: strings.TrimRight(cfg.PublicURL, "/"),
	}, nil
}

// Upload stores body under key. The request needs the body's length up
// front: io.Seekers and types with a Len method (*os.File, multipart.File,
// *bytes.Reader, ...) report it, anything else is read into memory first,
// so cap an untrusted body (http.MaxBytesReader) before passing it in.
func (r *R2) Upload(ctx context.Context, key string, body io.Reader, contentType string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	switch body.(type) {
	case io.Seeker, interface{ Len() int }:
	default:
		data, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("storage: read upload body: %w", err)
		}
		body = bytes.NewReader(data)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	if _, err := r.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("storage: put object: %w", err)
	}
	return nil
}

// Delete removes the object at key. R2 answers a delete of a missing key
// with success, so Delete is idempotent.
func (r *R2) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: delete object: %w", err)
	}
	return nil
}

// PresignPut returns a URL a client can PUT the object to directly. The
// Content-Type header is part of the signature, so the client must send
// exactly contentType; that is why it is required here.
func (r *R2) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	if contentType == "" {
		return "", errors.New("storage: content type is required for a presigned upload")
	}
	if err := validateTTL(ttl); err != nil {
		return "", err
	}

	req, err := r.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}
	return req.URL, nil
}

// PresignGet returns a URL that downloads the object until ttl elapses.
func (r *R2) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	if err := validateTTL(ttl); err != nil {
		return "", err
	}

	req, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign get: %w", err)
	}
	return req.URL, nil
}

// PublicURL returns Config.PublicURL joined with the escaped key. It
// returns "" when no public URL is configured or the key is invalid.
func (r *R2) PublicURL(key string) string {
	if r.publicURL == "" || ValidateKey(key) != nil {
		return ""
	}

	segments := strings.Split(key, "/")
	for i, s := range segments {
		// PathEscape leaves "+" alone, but some servers read it as a space.
		segments[i] = strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
	}
	return r.publicURL + "/" + strings.Join(segments, "/")
}

func validateTTL(ttl time.Duration) error {
	if ttl < time.Second || ttl > maxPresignTTL {
		return errors.New("storage: presign ttl must be between 1 second and 7 days")
	}
	return nil
}
