// Package storagetest is the conformance suite every artifact storage must pass.
package storagetest

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

// Run executes the suite against s. Keys are prefixed with prefix so that runs do not collide.
func Run(t *testing.T, s plugin.Storage, prefix string) {
	ctx := t.Context()
	key := prefix + "w1/r1/run1/vendas-por-regiao 2026-09-25.csv"
	body := []byte("id;região\r\n1;Sul\r\n")
	meta := plugin.ObjectMeta{ContentType: "text/csv; charset=utf-8", FileName: "vendas-por-regiao.csv"}

	t.Run("put and get", func(t *testing.T) {
		if err := s.Put(ctx, key, bytes.NewReader(body), meta); err != nil {
			t.Fatal(err)
		}
		r, got, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Close() }()
		b, _ := io.ReadAll(r)
		if !bytes.Equal(b, body) || got.Size != int64(len(body)) || got.ContentType != meta.ContentType {
			t.Errorf("got %q %+v", b, got)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		if err := s.Put(ctx, key, bytes.NewReader([]byte("new")), meta); err != nil {
			t.Fatal(err)
		}
		r, _, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		if string(b) != "new" {
			t.Errorf("got %q", b)
		}
	})

	t.Run("large object", func(t *testing.T) {
		big := make([]byte, 6<<20)
		_, _ = rand.Read(big)
		k := prefix + "w1/r1/run1/big.bin"
		if err := s.Put(ctx, k, bytes.NewReader(big), plugin.ObjectMeta{ContentType: "application/octet-stream"}); err != nil {
			t.Fatal(err)
		}
		r, m, err := s.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		n, _ := io.Copy(h, r)
		_ = r.Close()
		want := sha256.Sum256(big)
		if n != int64(len(big)) || m.Size != n || !bytes.Equal(h.Sum(nil), want[:]) {
			t.Errorf("large object differs: %d bytes, meta %+v", n, m)
		}
	})

	t.Run("presign", func(t *testing.T) {
		url, err := s.PresignGet(ctx, key, time.Minute, meta)
		if errors.Is(err, plugin.ErrPresignUnsupported) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.Get(url) //nolint:gosec,noctx // the URL comes from the storage under test
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || string(b) != "new" {
			t.Errorf("presigned GET: %d %q", res.StatusCode, b)
		}
	})

	t.Run("missing and delete", func(t *testing.T) {
		if _, _, err := s.Get(ctx, prefix+"nope/x.csv"); !errors.Is(err, plugin.ErrObjectNotFound) {
			t.Errorf("missing object: %v", err)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Get(ctx, key); !errors.Is(err, plugin.ErrObjectNotFound) {
			t.Errorf("deleted object: %v", err)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Errorf("deleting twice: %v", err)
		}
	})

	t.Run("unsafe keys", func(t *testing.T) {
		for _, k := range []string{"../escape.csv", "/abs.csv", prefix + "a/../../b.csv", ""} {
			if err := s.Put(ctx, k, bytes.NewReader(body), meta); err == nil {
				t.Errorf("key %q was accepted", k)
			}
		}
	})
}
