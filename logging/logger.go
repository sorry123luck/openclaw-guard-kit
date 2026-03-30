//go:build windows

package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger represents a structured logger.
type Logger struct {
	mu   sync.Mutex
	core *log.Logger
	file *os.File
}

// New creates a new Logger that writes to the given log path and standard output.
func New(logPath string) (*Logger, error) {
	writers := []io.Writer{os.Stdout}
	var file *os.File
	var err error
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, err
		}
		file, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		writers = append(writers, file)
	}

	return &Logger{
		core: log.New(io.MultiWriter(writers...), "", 0),
		file: file,
	}, nil
}

// Close closes the logger's file if it was set.
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// Debug logs a debug message.
func (l *Logger) Debug(msg string, kv ...any) { l.write("DEBUG", msg, kv...) }

// Info logs an info message.
func (l *Logger) Info(msg string, kv ...any) { l.write("INFO", msg, kv...) }

// Error logs an error message.
func (l *Logger) Error(msg string, kv ...any) { l.write("ERROR", msg, kv...) }

// write outputs a formatted log line.
func (l *Logger) write(level, msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	line := fmt.Sprintf("%s [%s] %s", time.Now().UTC().Format(time.RFC3339), level, msg)
	if len(kv) > 0 {
		line += " |"
	}
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprintf("arg%d", i)
		if i < len(kv) {
			key = fmt.Sprint(kv[i])
		}
		value := "<missing>"
		if i+1 < len(kv) {
			value = fmt.Sprint(kv[i+1])
		}
		line += fmt.Sprintf(" %s=%s", key, value)
	}
	l.core.Println(line)
}

// CoreLogger returns the underlying *log.Logger for use with other packages that expect the standard logger.
// It returns nil if the receiver is nil.
func (l *Logger) CoreLogger() *log.Logger {
	if l == nil {
		return nil
	}
	return l.core
}
