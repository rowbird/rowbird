// Package storage validates artifact keys, shared by the storage backends in its subpackages.
package storage

import (
	"errors"
	"path"
	"strings"
)

// ErrInvalidKey is returned for keys that are empty, absolute or climb out of the store.
var ErrInvalidKey = errors.New("storage: invalid key")

// CheckKey accepts relative slash-separated keys without "." or ".." segments.
func CheckKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.ContainsRune(key, 0) {
		return ErrInvalidKey
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return ErrInvalidKey
		}
	}
	if path.Clean(key) != key {
		return ErrInvalidKey
	}
	return nil
}
