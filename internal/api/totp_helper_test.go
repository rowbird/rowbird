package api

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
