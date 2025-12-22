package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
	"github.com/tuxedocurly/wledger/internal/config"

)

// New() initializes a new slog.Logger that writes to both stdout and a file
func New(debug bool) *slog.Logger {
	// Create the log directory if it doesn't exist
	if err := os.MkdirAll(config.DirLogs, 0755); err != nil {
		// If logDir can't be created, I won't be able to log to a file - So panic during startup
		panic("failed to create log directory: " + err.Error())
	}

	// Configure log rotation parameters using Lumberjack
	fileWriter := &lumberjack.Logger{
		Filename:   filepath.Join(config.DirLogs, "app.log"),
		MaxSize:    5,   // Megabytes
		MaxBackups: 3,    // Number of files
		MaxAge:     14,   // Days
		Compress:   true, // Compres old logs
	}

	// Configure Log Level
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	// Create the MultiWriter for Console + File
	// Use a text handler for pretty console output, and JSON for a parsable file
	jsonHandler := slog.NewJSONHandler(io.MultiWriter(os.Stdout, fileWriter), &slog.HandlerOptions{
		Level: level,
		// AddSource adds the file:line to the output for easier debugging
		AddSource: debug,
	})

	logger := slog.New(jsonHandler)

	// Set as global default so that slog.Info() works everywhere
	slog.SetDefault(logger)

	return logger
}
