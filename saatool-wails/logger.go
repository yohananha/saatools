package main

import (
	"strings"
	"sync"
)

const maxLogLines = 500

// MemLogger is an io.Writer that keeps the last maxLogLines lines in memory.
type MemLogger struct {
	mu    sync.Mutex
	lines []string
	buf   string
}

var globalMemLogger = &MemLogger{}

// GetMemLogger returns the singleton MemLogger.
func GetMemLogger() *MemLogger {
	return globalMemLogger
}

// Write implements io.Writer. It splits on newlines and stores each line.
func (m *MemLogger) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.buf += string(p)
	for {
		idx := strings.IndexByte(m.buf, '\n')
		if idx < 0 {
			break
		}
		line := m.buf[:idx]
		m.buf = m.buf[idx+1:]
		if line != "" {
			m.lines = append(m.lines, line)
			if len(m.lines) > maxLogLines {
				m.lines = m.lines[len(m.lines)-maxLogLines:]
			}
		}
	}
	return len(p), nil
}

// GetLines returns a copy of all captured log lines.
func (m *MemLogger) GetLines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]string, len(m.lines))
	copy(result, m.lines)
	return result
}
