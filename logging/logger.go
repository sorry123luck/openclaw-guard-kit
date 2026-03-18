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

type Logger struct {
	mu   sync.Mutex
	core *log.Logger
	file *os.File
}

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

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) Debug(msg string, kv ...any) { l.write("DEBUG", msg, kv...) }
func (l *Logger) Info(msg string, kv ...any)  { l.write("INFO", msg, kv...) }
func (l *Logger) Error(msg string, kv ...any) { l.write("ERROR", msg, kv...) }

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
