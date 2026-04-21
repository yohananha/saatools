package main

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxLogLines = 500

// errorWebhookURL is the ntfy.sh topic that receives automatic error alerts.
const errorWebhookURL = "https://ntfy.sh/babel-reader"

// webhookCooldown is the minimum interval between consecutive notifications.
const webhookCooldown = 60 * time.Second

// webhookRetryDelays are the wait times between successive retry attempts
// when the POST fails (e.g. no internet). Retries stop after the last delay.
var webhookRetryDelays = []time.Duration{
	15 * time.Second,
	60 * time.Second,
	5 * time.Minute,
	1 * time.Hour,
	1 * time.Hour,
	1 * time.Hour,
	1 * time.Hour,
	1 * time.Hour,
}

// MemLogger is an io.Writer that keeps the last maxLogLines lines in memory
// and fires an HTTP webhook whenever an error line is detected.
type MemLogger struct {
	mu    sync.Mutex
	lines []string
	buf   string

	webhookMu     sync.Mutex
	lastWebhookAt time.Time
}

var globalMemLogger = &MemLogger{}

// GetMemLogger returns the singleton MemLogger.
func GetMemLogger() *MemLogger {
	return globalMemLogger
}

// Write implements io.Writer. It splits on newlines, stores each line,
// and triggers a webhook notification for lines containing "error".
func (m *MemLogger) Write(p []byte) (n int, err error) {
	m.mu.Lock()

	m.buf += string(p)
	var newLines []string
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
			newLines = append(newLines, line)
		}
	}

	// Snapshot recent lines while still holding mu.
	var snapshot []string
	if len(newLines) > 0 {
		snapshot = make([]string, len(m.lines))
		copy(snapshot, m.lines)
	}

	m.mu.Unlock()

	// Check if any new line is an error and fire the webhook (once per cooldown).
	for _, line := range newLines {
		if strings.Contains(strings.ToLower(line), "error") {
			m.webhookMu.Lock()
			ready := time.Since(m.lastWebhookAt) >= webhookCooldown
			if ready {
				m.lastWebhookAt = time.Now()
			}
			m.webhookMu.Unlock()

			if ready {
				start := len(snapshot) - 30
				if start < 0 {
					start = 0
				}
				snippet := strings.Join(snapshot[start:], "\n")
				go fireWebhookWithRetry(line, snippet)
			}
			break // one notification per Write call is enough
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

// SendTestNotification fires a test webhook immediately, bypassing the cooldown.
func (m *MemLogger) SendTestNotification() {
	go fireWebhookWithRetry("test error: manual notification test", "This is a test from SaaTools — if you see this, error notifications are working.")
}

// fireWebhookWithRetry attempts to POST the notification and retries on failure
// with increasing delays so offline devices eventually deliver the alert.
func fireWebhookWithRetry(errorLine, recentLog string) {
	body := errorLine + "\n\nRecent log:\n" + recentLog
	if tryPost(body) {
		return
	}
	for _, delay := range webhookRetryDelays {
		time.Sleep(delay)
		if tryPost(body) {
			return
		}
	}
}

// tryPost sends one POST to errorWebhookURL and returns true on HTTP success.
func tryPost(body string) bool {
	req, err := http.NewRequest(http.MethodPost, errorWebhookURL, strings.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Title", "SaaTools Error")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode < 500
}
