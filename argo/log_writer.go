package argo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"x-ui/config"
	"x-ui/logger"
)

// LogWriter accumulates cloudflared output (for quick-tunnel domain parsing)
// while forwarding meaningful lines to the panel logger. Optionally mirrors
// everything to a per-tag log file under the bin folder.
type LogWriter struct {
	mu         sync.Mutex
	buffer     strings.Builder
	lastLine   string
	logFile    *os.File
	domainRe   *regexp.Regexp
	crashRe    *regexp.Regexp
}

func NewLogWriter(logPath string) *LogWriter {
	lw := &LogWriter{
		domainRe: regexp.MustCompile(`[a-z0-9][a-z0-9-]{0,61}\.trycloudflare\.com`),
		crashRe:  regexp.MustCompile(`(?i)(panic|fatal|tunnel registration failed)`),
	}
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err == nil {
			if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				lw.logFile = f
			}
		}
	}
	return lw
}

func (lw *LogWriter) Write(m []byte) (int, error) {
	msg := strings.TrimSpace(string(m))
	lw.mu.Lock()
	lw.buffer.WriteString(msg)
	lw.mu.Unlock()

	if lw.logFile != nil {
		_, _ = lw.logFile.WriteString(time.Now().Format("2006-01-02 15:04:05 ") + msg + "\n")
	}

	if lw.crashRe.MatchString(msg) {
		logger.Warning("ARGO: " + msg)
		lw.lastLine = msg
		return len(m), nil
	}
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), "error") {
			logger.Error("ARGO: " + line)
		} else if strings.Contains(strings.ToLower(line), "registered tunnel") ||
			strings.Contains(line, "trycloudflare.com") {
			logger.Info("ARGO: " + line)
		} else {
			logger.Debug("ARGO: " + line)
		}
		lw.lastLine = line
	}
	return len(m), nil
}

// ExtractDomain returns the first trycloudflare.com address seen so far.
func (lw *LogWriter) ExtractDomain() string {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	m := lw.domainRe.FindString(lw.buffer.String())
	if m == "" {
		return ""
	}
	return "https://" + m
}

// Reset clears the accumulated buffer (used before each tunnel start).
func (lw *LogWriter) Reset() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.buffer.Reset()
	lw.lastLine = ""
}

func (lw *LogWriter) Close() {
	if lw.logFile != nil {
		_ = lw.logFile.Close()
		lw.logFile = nil
	}
}

// writeCrashReport 将 cloudflared 崩溃日志落盘到 bin/ 目录便于排查
func writeCrashReport(m []byte) {
	binFolder := config.GetBinFolderPath()
	reportFile := filepath.Join(binFolder, "argo_crash_report.log")
	if dirErr := os.MkdirAll(binFolder, 0755); dirErr != nil {
		logger.Error("Unable to create bin folder for crash report:", dirErr)
		return
	}
	f, err := os.OpenFile(reportFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Error("Unable to open crash report file:", err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(time.Now().Format("2006-01-02 15:04:05") + "\n" + string(m) + "\n")
}
