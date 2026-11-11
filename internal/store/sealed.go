package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrWrongKey is the secret key not being the one the settings were saved with.
var ErrWrongKey = errors.New("extension settings can't be decrypted: TOMTLY_SECRET_KEY isn't the key they were saved with. Set the right one, or delete the extensions' rows to enter their settings again")

// sealer encrypts extension settings with AES-256-GCM. The source is bound in, so a row moved to another source fails.
type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) (sealer, error) {
	if len(key) != 32 {
		return sealer{}, fmt.Errorf("the secret key must be 32 bytes, it is %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return sealer{}, err
	}
	aead, err := cipher.NewGCM(block)
	return sealer{aead}, err
}

func (s sealer) seal(source string, settings map[string]any) ([]byte, error) {
	plain, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(source)), nil
}

func (s sealer) open(source string, sealed []byte) (map[string]any, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrWrongKey
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(source))
	if err != nil {
		return nil, ErrWrongKey
	}
	var settings map[string]any
	return settings, json.Unmarshal(plain, &settings)
}

// sealOldRows encrypts settings saved before they were encrypted, then checks the key opens what is already sealed, so a
// wrong key stops the service at start instead of every extension failing on its own.
func (p *Postgres) sealOldRows(ctx context.Context) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT source, settings FROM added_extensions WHERE sealed IS NULL FOR UPDATE`)
		if err != nil {
			return err
		}
		old, err := pgx.CollectRows(rows, pgx.RowToStructByPos[AddedExtension])
		if err != nil {
			return err
		}
		for _, e := range old {
			sealed, err := p.sealer.seal(e.Source, e.Settings)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE added_extensions SET sealed = $2, settings = NULL WHERE source = $1`, e.Source, sealed); err != nil {
				return err
			}
		}
		var source string
		var sealed []byte
		err = tx.QueryRow(ctx, `SELECT source, sealed FROM added_extensions LIMIT 1`).Scan(&source, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = p.sealer.open(source, sealed)
		return err
	})
}
