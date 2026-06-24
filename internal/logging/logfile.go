package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type fileWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func (w *fileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		w.f = f
	}
	return w.f.Write(p)
}

func (w *fileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Setup configures standard log output to stderr and an optional log file.
// logFile empty uses dataDir/sps.log. Set logFile to "none" to disable file logging.
func Setup(dataDir, logFile string) (io.Closer, error) {
	logFile = strings.TrimSpace(logFile)
	if logFile == "" {
		logFile = filepath.Join(dataDir, "sps.log")
	}
	if strings.EqualFold(logFile, "none") {
		return noopCloser{}, nil
	}
	if strings.HasPrefix(logFile, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		logFile = filepath.Join(home, logFile[2:])
	}
	fw := &fileWriter{path: logFile}
	log.SetOutput(io.MultiWriter(os.Stderr, fw))
	log.Printf("logging to %s", logFile)
	return fw, nil
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// ResolvePath returns the effective log file path for display.
func ResolvePath(dataDir, logFile string) string {
	logFile = strings.TrimSpace(logFile)
	if logFile == "" {
		return filepath.Join(dataDir, "sps.log")
	}
	if strings.EqualFold(logFile, "none") {
		return ""
	}
	if strings.HasPrefix(logFile, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, logFile[2:])
	}
	return logFile
}

// Tail reads the last n lines from a log file.
func Tail(path string, n int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("(log file not found: %s)", path), nil
		}
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
