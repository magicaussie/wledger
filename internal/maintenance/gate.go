package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// Gate coordinates application requests with restore activation, both in this
// process and with other WLEDger processes sharing the same uploads directory.
type Gate struct {
	mu         sync.RWMutex
	uploadsDir string
	lockPath   string
	poisoned   atomic.Bool
}

const (
	// JournalName is the durable restore journal file name.
	JournalName = ".wledger-restore-journal.json"
	// InMountControlDirName is the name of the hidden control directory created
	// inside a mounted uploads directory. It lives inside the mount so restore
	// recovery metadata survives container recreation, unlike the sibling control
	// directory (ControlDir), which lives on the container filesystem.
	InMountControlDirName = ".wledger-restore"
	// maintenanceLockName is the cross-process maintenance lock file name.
	maintenanceLockName = ".wledger-maintenance.lock"
)

func ControlDir(uploadsDir string) string {
	absolute, err := filepath.Abs(uploadsDir)
	if err != nil {
		absolute = filepath.Clean(uploadsDir)
	}
	root := filepath.Dir(absolute)
	digest := sha256.Sum256([]byte(absolute))
	return filepath.Join(root, ".wledger-restore-control-"+hex.EncodeToString(digest[:12]))
}

// InMountControlDir returns the hidden control directory inside a mounted
// uploads directory. It is the persistent home of the restore journal and the
// generation staging directories when the uploads path is a mount point.
func InMountControlDir(uploadsDir string) string {
	return filepath.Join(uploadsDir, InMountControlDirName)
}

// JournalPaths returns every location a pending restore journal may occupy for
// this uploads directory, most durable first: the in-mount location (used when
// the uploads path is a mount point, so it survives container recreation) and
// the sibling control-directory location (used for an ordinary uploads
// directory). Callers fail closed if any of them exists.
func JournalPaths(uploadsDir string) []string {
	return []string{
		filepath.Join(InMountControlDir(uploadsDir), JournalName),
		filepath.Join(ControlDir(uploadsDir), JournalName),
	}
}

// mountPointDetector is a test seam over IsMountPoint so the mount-aware lock
// placement can be exercised without creating a real mount.
var mountPointDetector = IsMountPoint

// LockPath returns the location of the cross-process maintenance lock. When the
// uploads path is a mount point the lock lives inside the persistent mount, so
// every container sharing that bind mount contends on the same lock file and
// cannot run a restore concurrently with another container. It is a hidden file
// at the mount root: the mount-aware restore protocol never swaps hidden
// entries, so the lock is never moved, and the uploads route never serves it.
// For an ordinary uploads directory the lock lives in the sibling control
// directory, because that directory is renamed wholesale during a restore.
func LockPath(uploadsDir string) string {
	if mountPointDetector(uploadsDir) {
		return filepath.Join(uploadsDir, maintenanceLockName)
	}
	return filepath.Join(ControlDir(uploadsDir), maintenanceLockName)
}

func New(uploadsDir string) *Gate {
	return &Gate{
		uploadsDir: uploadsDir,
		lockPath:   LockPath(uploadsDir),
	}
}

func (g *Gate) lock(mode int) (func(), error) {
	g.mu.Lock()
	if err := os.MkdirAll(filepath.Dir(g.lockPath), 0700); err != nil {
		g.mu.Unlock()
		return nil, fmt.Errorf("create maintenance lock directory: %w", err)
	}
	f, err := os.OpenFile(g.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		g.mu.Unlock()
		return nil, fmt.Errorf("open maintenance lock %q: %w", g.lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		_ = f.Close()
		g.mu.Unlock()
		return nil, fmt.Errorf("acquire maintenance lock %q: %w", g.lockPath, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		g.mu.Unlock()
	}, nil
}

func (g *Gate) ReadLock() (func(), error) {
	g.mu.RLock()
	if err := os.MkdirAll(filepath.Dir(g.lockPath), 0700); err != nil {
		g.mu.RUnlock()
		return nil, fmt.Errorf("create maintenance lock directory: %w", err)
	}
	f, err := os.OpenFile(g.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		g.mu.RUnlock()
		return nil, fmt.Errorf("open maintenance lock %q: %w", g.lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		_ = f.Close()
		g.mu.RUnlock()
		return nil, fmt.Errorf("acquire maintenance read lock %q: %w", g.lockPath, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		g.mu.RUnlock()
	}, nil
}

func (g *Gate) WriteLock() (func(), error) {
	return g.lock(syscall.LOCK_EX)
}

func (g *Gate) Poison() { g.poisoned.Store(true) }

func (g *Gate) Unavailable() bool { return g.poisoned.Load() }

func (g *Gate) hasPendingRestore() bool {
	for _, path := range JournalPaths(g.uploadsDir) {
		if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.poisoned.Load() {
			http.Error(w, "WLEDger is unavailable while restore recovery is required; restart the service to recover the pending restore", http.StatusServiceUnavailable)
			return
		}
		// The restore handler acquires the exclusive lock itself after authenticating
		// the administrator and parsing the bounded upload body.
		if r.Method == http.MethodPost && r.URL.Path == "/settings/backup/restore" {
			next.ServeHTTP(w, r)
			return
		}
		unlock, err := g.ReadLock()
		if err != nil {
			http.Error(w, "WLEDger maintenance lock is unavailable", http.StatusServiceUnavailable)
			return
		}
		defer unlock()
		if g.poisoned.Load() || g.hasPendingRestore() {
			http.Error(w, "WLEDger is unavailable while restore recovery is required; restart the service to recover the pending restore", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
