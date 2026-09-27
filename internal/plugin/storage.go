package plugin

import (
	"context"
	"errors"
	"io"
	"time"
)

// Storage keeps artifacts: the files a run produces for its deliveries and links
// (docs/spec/04-plugins.md, "Artifact storage").
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, meta ObjectMeta) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error)
	Delete(ctx context.Context, key string) error
	// PresignGet returns a short-lived URL to download key directly, or ErrPresignUnsupported.
	PresignGet(ctx context.Context, key string, ttl time.Duration, meta ObjectMeta) (string, error)
}

// ObjectMeta describes a stored object.
type ObjectMeta struct {
	ContentType string
	Size        int64
	// FileName is offered when the object is downloaded.
	FileName string
}

// Storage errors.
var (
	ErrObjectNotFound     = errors.New("storage: object not found")
	ErrPresignUnsupported = errors.New("storage: presigned URLs are not supported")
)
