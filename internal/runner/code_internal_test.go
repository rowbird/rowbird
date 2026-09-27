package runner

import (
	"fmt"
	"testing"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/plugin"
)

func TestCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&plugin.ConnError{Code: plugin.ErrCodeAuthFailed}, plugin.ErrCodeAuthFailed},
		{fmt.Errorf("open: %w", apperr.New(apperr.KindInvalid, plugin.ErrCodePathNotAllowed)), plugin.ErrCodePathNotAllowed},
		{fmt.Errorf("something broke"), CodeInternal},
	}
	for _, c := range cases {
		if got := codeOf(c.err); got != c.want {
			t.Errorf("codeOf(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
