package logger

import (
	"log/slog"
	"os"
	"testing"
)

func TestDynamicLogLevel(t *testing.T) {
	// Initial State - Info level
	SetDebug(false)
	if LogLevel.Level() != slog.LevelInfo {
		t.Errorf("expected level to be Info, got %v", LogLevel.Level())
	}

	// Switch to Debug
	SetDebug(true)
	if LogLevel.Level() != slog.LevelDebug {
		t.Errorf("expected level to be Debug, got %v", LogLevel.Level())
	}

	// Switch back to Info
	SetDebug(false)
	if LogLevel.Level() != slog.LevelInfo {
		t.Errorf("expected level to be Info, got %v", LogLevel.Level())
	}
}

func TestNew(t *testing.T) {
	// Cleanup the created app directory after test
	t.Cleanup(func() {
		os.RemoveAll("app")
	})

	// Testing New() mainly for coverage as it has side effects (mkdir, file write)
	l1 := New(true)
	if l1 == nil {
		t.Fatal("expected logger to be initialized, got nil")
	}

	l2 := New(false)
	if l2 == nil {
		t.Fatal("expected logger to be initialized, got nil")
	}
}
