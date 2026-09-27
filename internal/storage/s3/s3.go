// Package s3 stores artifacts in an S3-compatible bucket (AWS S3, MinIO, Cloudflare R2, ...).
// Downloads redirect to short-lived presigned URLs, so large files do not pass through Rowbird.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/storage"
)

// MaxPresignTTL bounds presigned URLs (docs/spec/04-plugins.md: at most 5 minutes).
const MaxPresignTTL = 5 * time.Minute

// Config is an S3 location. Endpoint is a URL; its scheme decides TLS.
type Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey security.Secret
	SecretKey security.Secret
	PathStyle bool
	// Prefix is prepended to every key (for sharing a bucket).
	Prefix string
	// Transport, when set, carries the requests (the S3 destination dials through the network
	// policy); MaxRetries bounds minio's own retries (0 keeps its default).
	Transport  http.RoundTripper
	MaxRetries int
}

// Storage is a bucket.
type Storage struct {
	client *minio.Client
	bucket string
	prefix string
}

// New connects to the bucket described by cfg. It does not create the bucket.
func New(cfg Config) (*Storage, error) {
	client, err := Client(cfg)
	if err != nil {
		return nil, err
	}
	return &Storage{client: client, bucket: cfg.Bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

// Client builds a minio client for cfg; the S3 destination uses it too.
func Client(cfg Config) (*minio.Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("s3: endpoint must be an http or https URL")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	return minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey.Reveal(), cfg.SecretKey.Reveal(), ""),
		Secure:       u.Scheme == "https",
		Region:       cfg.Region,
		BucketLookup: lookup,
		Transport:    cfg.Transport,
		MaxRetries:   cfg.MaxRetries,
	})
}

func (s *Storage) object(key string) (string, error) {
	if err := storage.CheckKey(key); err != nil {
		return "", err
	}
	if s.prefix == "" {
		return key, nil
	}
	return s.prefix + "/" + key, nil
}

func disposition(name string) string {
	if name == "" {
		return ""
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// Put uploads an object; unknown sizes are streamed in parts.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, meta plugin.ObjectMeta) error {
	obj, err := s.object(key)
	if err != nil {
		return err
	}
	size := int64(-1)
	if meta.Size > 0 {
		size = meta.Size
	}
	_, err = s.client.PutObject(ctx, s.bucket, obj, r, size, minio.PutObjectOptions{
		ContentType: meta.ContentType, ContentDisposition: disposition(meta.FileName),
	})
	return mapError(err)
}

// Get opens an object.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, plugin.ObjectMeta, error) {
	var meta plugin.ObjectMeta
	obj, err := s.object(key)
	if err != nil {
		return nil, meta, err
	}
	o, err := s.client.GetObject(ctx, s.bucket, obj, minio.GetObjectOptions{})
	if err != nil {
		return nil, meta, mapError(err)
	}
	info, err := o.Stat()
	if err != nil {
		_ = o.Close()
		return nil, meta, mapError(err)
	}
	return o, plugin.ObjectMeta{ContentType: info.ContentType, Size: info.Size}, nil
}

// Delete removes an object; a missing object is not an error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	obj, err := s.object(key)
	if err != nil {
		return err
	}
	return mapError(s.client.RemoveObject(ctx, s.bucket, obj, minio.RemoveObjectOptions{}))
}

// PresignGet returns a URL valid for at most MaxPresignTTL that downloads the object with its file
// name.
func (s *Storage) PresignGet(ctx context.Context, key string, ttl time.Duration, meta plugin.ObjectMeta) (string, error) {
	obj, err := s.object(key)
	if err != nil {
		return "", err
	}
	params := url.Values{}
	if d := disposition(meta.FileName); d != "" {
		params.Set("response-content-disposition", d)
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, obj, min(ttl, MaxPresignTTL), params)
	if err != nil {
		return "", mapError(err)
	}
	return u.String(), nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if r := minio.ToErrorResponse(err); r.Code == "NoSuchKey" || r.Code == "NotFound" {
		return plugin.ErrObjectNotFound
	}
	return err
}
