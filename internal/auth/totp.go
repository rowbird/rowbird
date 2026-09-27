package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters (RFC 6238 defaults, which every authenticator app supports).
const (
	totpPeriod = 30
	totpDigits = otp.DigitsSix
	totpSkew   = 1 // accept one step before and after, for clock drift
)

// TOTPEnrollment is what the user needs to add Rowbird to an authenticator app.
type TOTPEnrollment struct {
	Secret    string
	URL       string
	QRCodePNG string // data URL
}

// NewTOTPEnrollment generates a secret for accountName (the user's email).
func NewTOTPEnrollment(issuer, accountName string) (*TOTPEnrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   otp.AlgorithmSHA1,
		SecretSize:  20,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: generate TOTP secret: %w", err)
	}
	img, err := key.Image(240, 240)
	if err != nil {
		return nil, fmt.Errorf("auth: render TOTP QR code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("auth: encode TOTP QR code: %w", err)
	}
	return &TOTPEnrollment{
		Secret:    key.Secret(),
		URL:       key.URL(),
		QRCodePNG: "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// MatchTOTP checks code against secret at time t and returns the matching time step. Callers must
// reject a step that is not newer than the last one used (replay protection).
func MatchTOTP(secret, code string, t time.Time) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits.Length() {
		return 0, false
	}
	current := t.Unix() / totpPeriod
	for delta := int64(-totpSkew); delta <= totpSkew; delta++ {
		s := current + delta
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*totpPeriod, 0), totp.ValidateOpts{
			Period: totpPeriod, Digits: totpDigits, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
