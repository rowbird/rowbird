// Package keys rotates the master key (docs/spec/07-security.md, "Secrets & encryption"): every
// encrypted value is decrypted with the keys in use and encrypted again with the new key, in one
// transaction, so a failure leaves the database as it was.
package keys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
)

// EventKeysRotated is the security event recorded in every workspace after a rotation.
const EventKeysRotated = "keys_rotated"

// liveWindow is how recent an instance heartbeat must be for the instance to count as running.
const liveWindow = 2 * time.Minute

// ErrInstancesRunning means instances that cannot read the new key are still running.
var ErrInstancesRunning = errors.New("keys: running instances do not use the new key yet")

// Result counts the values of a rotation by kind. Current counts values already encrypted with the
// new key, which a repeated rotation leaves alone.
type Result struct {
	Rotated map[string]int
	Current int
	NewKey  string
}

// Total is the number of values encrypted again.
func (r Result) Total() int {
	n := 0
	for _, v := range r.Rotated {
		n += v
	}
	return n
}

// Options control a rotation.
type Options struct {
	// DryRun decrypts and encrypts every value and then rolls back.
	DryRun bool
	// Force skips the check of running instances.
	Force bool
	Now   func() time.Time
}

var errDryRun = errors.New("keys: dry run")

// Rotate encrypts every value again with newKey. current must decrypt every stored value (the key
// in use, plus the previous one while an earlier rotation is being rolled out).
func Rotate(ctx context.Context, st *store.Store, current *crypto.Keyring, newKey []byte, opts Options) (Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	target, err := crypto.NewKeyring(newKey)
	if err != nil {
		return Result{}, err
	}
	res := Result{Rotated: map[string]int{}, NewKey: target.PrimaryKeyID()}
	if !opts.Force {
		if err := checkInstances(ctx, st, target.PrimaryKeyID(), now()); err != nil {
			return res, err
		}
	}
	err = st.RunInTx(ctx, func(ctx context.Context) error {
		values, err := st.SecretValues().List(ctx)
		if err != nil {
			return err
		}
		for _, v := range values {
			if id, err := crypto.KeyIDOf(v.Value); err == nil && id == target.PrimaryKeyID() {
				res.Current++
				continue
			}
			aad, err := associatedData(v)
			if err != nil {
				return err
			}
			plain, err := current.Decrypt(v.Value, aad)
			if err != nil {
				return fmt.Errorf("decrypt %s %s: %w", v.Kind, v.ID, err)
			}
			enc, err := target.Encrypt(plain, aad)
			clear(plain)
			if err != nil {
				return err
			}
			if err := st.SecretValues().Replace(ctx, v, enc); err != nil {
				return err
			}
			res.Rotated[v.Kind]++
		}
		if opts.DryRun {
			return errDryRun
		}
		return recordEvents(ctx, st, res)
	})
	if errors.Is(err, errDryRun) {
		return res, nil
	}
	return res, err
}

// associatedData rebuilds what each kind of value was bound to when it was encrypted.
func associatedData(v store.SecretValue) ([]byte, error) {
	switch v.Kind {
	case store.SecretUserTOTP:
		return auth.TOTPAssociatedData(v.ID), nil
	case store.SecretConnection:
		return secretconfig.AssociatedData("connection", v.ID), nil
	case store.SecretChannel:
		return secretconfig.AssociatedData("channel", v.ID), nil
	case store.SecretSetting:
		switch v.Key {
		case store.SettingHeartbeatURL:
			return notify.HeartbeatAssociatedData(v.WorkspaceID), nil
		case ai.SettingSecrets:
			return secretconfig.AssociatedData(ai.SecretsKind, v.WorkspaceID), nil
		case auth.SettingOIDCSecret:
			return secretconfig.AssociatedData(auth.OIDCSecretKind, v.WorkspaceID), nil
		}
	}
	// A new kind of secret without a case here would otherwise be left on the old key.
	return nil, fmt.Errorf("keys: no associated data known for %s %q; refusing to rotate", v.Kind, v.Key)
}

func checkInstances(ctx context.Context, st *store.Store, newKeyID string, now time.Time) error {
	live, err := st.System().LiveInstances(ctx, now.Add(-liveWindow))
	if err != nil {
		return err
	}
	for _, in := range live {
		if in.KeyID != newKeyID {
			return fmt.Errorf("%w: instance %s on %s uses key %q; stop it, or restart it with the new key and the current one as ROWBIRD_MASTER_KEY_PREVIOUS (or pass --force)",
				ErrInstancesRunning, in.ID, in.Hostname, in.KeyID)
		}
	}
	return nil
}

func recordEvents(ctx context.Context, st *store.Store, res Result) error {
	workspaces, err := st.Workspaces().List(ctx)
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		wctx := store.WithWorkspace(ctx, w.ID)
		if err := st.SecurityEvents().Record(wctx, &store.SecurityEvent{
			Type: EventKeysRotated, Meta: map[string]any{"key_id": res.NewKey, "values": res.Total()},
		}); err != nil {
			return err
		}
	}
	return nil
}
