package singbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"x-ui/config"
	"x-ui/logger"
	"x-ui/util/common"
)

func GetBinaryName() string {
	return fmt.Sprintf("sing-box-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + GetBinaryName()
}

func GetConfigPath() string {
	return config.GetBinFolderPath() + "/singbox.json"
}

func stopProcess(p *Process) {
	p.Stop()
}

type Process struct {
	*process
}

func NewProcess(singboxConfig *Config) *Process {
	p := &Process{newProcess(singboxConfig)}
	runtime.SetFinalizer(p, stopProcess)
	return p
}

type process struct {
	cmd *exec.Cmd

	version string

	config    *Config
	logWriter *LogWriter
	exitErr   error
	startTime time.Time
}

func newProcess(config *Config) *process {
	return &process{
		version:   "Unknown",
		config:    config,
		logWriter: NewLogWriter(),
		startTime: time.Now(),
	}
}

func (p *process) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.cmd.ProcessState == nil {
		return true
	}
	return false
}

func (p *process) GetErr() error {
	return p.exitErr
}

func (p *process) GetResult() string {
	if len(p.logWriter.lastLine) == 0 && p.exitErr != nil {
		return p.exitErr.Error()
	}
	return p.logWriter.lastLine
}

func (p *process) GetConfig() *Config {
	return p.config
}

func (p *process) GetVersion() string {
	return p.version
}

func (p *process) GetUptime() uint64 {
	return uint64(time.Since(p.startTime).Seconds())
}

func (p *process) refreshVersion() {
	cmd := exec.Command(GetBinaryPath(), "version")
	data, err := cmd.Output()
	if err != nil {
		p.version = "Unknown"
	} else {
		// sing-box version 1.13.18
		datas := bytes.Split(bytes.TrimSpace(data), []byte(" "))
		if len(datas) <= 2 {
			p.version = "Unknown"
		} else {
			p.version = string(datas[2])
		}
	}
}

func (p *process) Start() (err error) {
	if p.IsRunning() {
		return errors.New("sing-box is already running")
	}

	defer func() {
		if err != nil {
			logger.Error("Failure in running sing-box process: ", err)
			p.exitErr = err
		}
	}()

	data, err := json.MarshalIndent(p.config, "", "  ")
	if err != nil {
		return common.NewErrorf("Failed to generate sing-box configuration files: %v", err)
	}

	err = os.MkdirAll(config.GetLogFolder(), 0o770)
	if err != nil {
		logger.Warningf("Failed to create log folder: %s", err)
	}

	configPath := GetConfigPath()
	err = os.WriteFile(configPath, data, fs.ModePerm)
	if err != nil {
		return common.NewErrorf("Failed to write sing-box configuration file: %v", err)
	}

	cmd := exec.Command(GetBinaryPath(), "run", "-c", configPath)
	p.cmd = cmd

	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter

	go func() {
		err := cmd.Run()
		if err != nil {
			logger.Error("Failure in running sing-box:", err)
			p.exitErr = err
		}
	}()

	p.refreshVersion()

	return nil
}

func (p *process) Stop() error {
	if !p.IsRunning() {
		return errors.New("sing-box is not running")
	}

	if runtime.GOOS == "windows" {
		return p.cmd.Process.Kill()
	} else {
		return p.cmd.Process.Signal(syscall.SIGTERM)
	}
}
