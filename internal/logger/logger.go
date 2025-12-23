package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tuxedocurly/wledger/internal/config"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogLevel is a dynamic level variable that can be changed at runtime
var LogLevel = &slog.LevelVar{}

// SetDebug toggles the log level between Debug and Info
func SetDebug(debug bool) {
	if debug {
		LogLevel.Set(slog.LevelDebug)
	} else {
		LogLevel.Set(slog.LevelInfo)
	}
}

// New() initializes a new slog.Logger that writes to both stdout and a file
func New(debug bool) *slog.Logger {
	// Initialize level
	SetDebug(debug)

	// Create the log directory if it doesn't exist
	if err := os.MkdirAll(config.DirLogs, 0755); err != nil {
		// If logDir can't be created, I won't be able to log to a file - So panic during startup
		panic("failed to create log directory: " + err.Error())
	}

	// Configure log rotation parameters using Lumberjack
	fileWriter := &lumberjack.Logger{
		Filename:   filepath.Join(config.DirLogs, "app.log"),
		MaxSize:    5,    // Megabytes
		MaxBackups: 3,    // Number of files
		MaxAge:     14,   // Days
		Compress:   true, // Compres old logs
	}

	// Create the MultiWriter for Console + File
	// Use a JSON handler for both for consistency in file,
	// though NewJSONHandler takes io.Writer which is MultiWritered.
	// NOTE: AddSource is set based on the initial debug flag and is not dynamic.
	handler := slog.NewJSONHandler(io.MultiWriter(os.Stdout, fileWriter), &slog.HandlerOptions{
		Level:     LogLevel,
		AddSource: debug,
	})

	logger := slog.New(handler)

	// Set as global default so that slog.Info() works everywhere
	slog.SetDefault(logger)

	return logger
}
