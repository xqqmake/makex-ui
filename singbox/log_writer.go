package singbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"x-ui/config"
	"x-ui/logger"
)

func NewLogWriter() *LogWriter {
	return &LogWriter{}
}

type LogWriter struct {
	lastLine string
}

func (lw *LogWriter) Write(m []byte) (n int, err error) {
	// sing-box 崩溃/致命错误日志特征（如 FATAL[0000] ...）
	crashRegex := regexp.MustCompile(`(?i)(panic|exception|stack trace|fatal error|fatal\[)`)

	// Convert the data to a string
	message := strings.TrimSpace(string(m))

	// Check if the message contains a crash
	if crashRegex.MatchString(message) {
		logger.Debug("sing-box crash detected:\n", message)
		lw.lastLine = message
		writeCrashReport(m)
		return len(m), nil
	}

	for _, msg := range strings.Split(message, "\n") {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		msgLower := strings.ToLower(msg)
		if strings.Contains(msgLower, "fatal") {
			logger.Error("SING-BOX: " + msg)
		} else if strings.Contains(msgLower, "warn") {
			logger.Warning("SING-BOX: " + msg)
		} else if strings.Contains(msgLower, "error") {
			logger.Error("SING-BOX: " + msg)
		} else if strings.Contains(msgLower, "info") {
			logger.Info("SING-BOX: " + msg)
		} else {
			logger.Debug("SING-BOX: " + msg)
		}
		lw.lastLine = msg
	}

	return len(m), nil
}

// writeCrashReport 将崩溃日志落盘到 bin/ 目录便于排查
func writeCrashReport(m []byte) {
	binFolder := config.GetBinFolderPath()
	reportFile := filepath.Join(binFolder, "singbox_crash_report.log")
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
	if _, err := f.WriteString(time.Now().Format("2006-01-02 15:04:05") + "\n" + string(m) + "\n"); err != nil {
		logger.Error("Unable to write crash report:", err)
	}
}
