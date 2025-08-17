package main_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Isolate cache/tmp per test *process* to avoid cross-process Dagger cache races on Windows.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "cu-stdio-cache-")
	if err != nil {
		panic(err)
	}
	cache := filepath.Join(root, "cache")
	tmp := filepath.Join(root, "tmp")
	_ = os.MkdirAll(cache, 0o755)
	_ = os.MkdirAll(tmp, 0o755)

	_ = os.Setenv("XDG_CACHE_HOME", cache)
	if runtime.GOOS == "windows" {
		_ = os.Setenv("TEMP", tmp)
		_ = os.Setenv("TMP", tmp)
	} else {
		_ = os.Setenv("TMPDIR", tmp)
	}

	if os.Getenv("TEST_VERBOSE") != "" {
		slog.Info("stdio test cache configured", "XDG_CACHE_HOME", cache, "tmp", tmp)
	}

	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
