package maintenance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type uploadClaimKey struct{}

// HoldsUploadReferences reports whether this call already owns the shared claim.
// Contexts carrying claims must not outlive the operation that acquired them.
func HoldsUploadReferences(ctx context.Context, dir string) bool {
	absolute, _ := filepath.Abs(dir)
	return ctx.Value(uploadClaimKey{}) == absolute
}

// UploadReferences must be acquired before opening a SQLite transaction and held
// until commit/rollback AND file publication finish. Nested document operations
// reuse the enclosing claim rather than acquiring locks inside a transaction.
func UploadReferences(ctx context.Context, dir string) (context.Context, func(), error) {
	if HoldsUploadReferences(ctx, dir) {
		return ctx, func() {}, nil
	}
	unlock, err := uploadLock(ctx, dir, syscall.LOCK_SH)
	if err != nil {
		return ctx, nil, err
	}
	absolute, _ := filepath.Abs(dir)
	return context.WithValue(ctx, uploadClaimKey{}, absolute), unlock, nil
}

// UploadCleanup excludes reference creation and restore during revalidation and
// deletion. Lock order: maintenance gate -> upload claim -> SQLite. Never upgrade
// a shared claim, or acquire a maintenance gate while holding an upload claim.
func UploadCleanup(ctx context.Context, dir string) (func(), error) {
	if HoldsUploadReferences(ctx, dir) {
		return nil, fmt.Errorf("cannot upgrade upload reference claim")
	}
	return uploadLock(ctx, dir, syscall.LOCK_EX)
}

func uploadLock(ctx context.Context, dir string, mode int) (func(), error) {
	// Use the same mount-aware, restore-stable control location as the gate, but
	// a distinct inode: ordinary HTTP requests already hold the maintenance gate.
	path := LockPath(dir) + ".uploads"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open upload claim: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			f.Close()
			return nil, fmt.Errorf("lock upload claim: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
