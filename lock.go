package agy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const lockRetryDelay = 50 * time.Millisecond

func (s *Store) LockCWD(ctx context.Context, cwd string) (func(), error) {
	return s.lock(ctx, "cwd-"+hashString(cwd))
}

func (s *Store) withSessionsLock(ctx context.Context, fn func() error) error {
	unlock, err := s.lock(ctx, "sessions")
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (s *Store) lock(ctx context.Context, name string) (func(), error) {
	path := filepath.Join(s.Dir, "locks", name+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(path)
	locked, err := lock.TryLockContext(ctx, lockRetryDelay)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, ctx.Err()
	}
	return func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}, nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
