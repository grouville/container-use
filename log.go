package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

var (
	logWriter io.Writer
)

func parseLogLevel(levelStr string) slog.Level {
	switch levelStr {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "info", "INFO":
		return slog.LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

var globalLog *os.File

func setupLogger() error {
	var writers []io.Writer
	writers = append(writers, os.Stderr)

	if logFile := os.Getenv("CU_STDERR_FILE"); logFile != "" {
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", logFile, err)
		}
		globalLog = file
		writers = append(writers, file)
	}

	logLevel := parseLogLevel(os.Getenv("CU_LOG_LEVEL"))
	logWriter = io.MultiWriter(writers...)
	handler := slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))

	return nil
}
