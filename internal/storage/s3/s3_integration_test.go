//go:build integration

package s3

import (
	"context"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rowbird/rowbird/internal/security"
	"github.com/rowbird/rowbird/internal/storage/storagetest"
)

// startS3 runs an S3-compatible server and creates bucket; it returns the endpoint and credentials.
// It uses the Versity S3 Gateway: MinIO no longer publishes community images.
func startS3(t *testing.T, bucket string) Config {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "versity/versitygw:v1.8.0",
			Entrypoint:   []string{"sh", "-c", "mkdir -p /data && exec versitygw --access rowbird --secret rowbird-secret --port :7070 posix /data"},
			ExposedPorts: []string{"7070/tcp"},
			WaitingFor:   wait.ForListeningPort("7070/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start s3 gateway: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	addr, err := c.PortEndpoint(ctx, "7070/tcp", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Endpoint: "http://" + addr, Region: "us-east-1", Bucket: bucket, AccessKey: security.Secret("rowbird"), SecretKey: security.Secret("rowbird-secret"), PathStyle: true}
	client, err := Client(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestConformance(t *testing.T) {
	cfg := startS3(t, "artifacts")
	cfg.Prefix = "rowbird"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	storagetest.Run(t, s, "")
}
