package argo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sync"
	"syscall"
	"time"

	"x-ui/config"
	"x-ui/logger"
	"x-ui/util/common"
)

func GetBinaryName() string {
	return fmt.Sprintf("cloudflared-linux-%s", runtime.GOARCH)
}

func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + GetBinaryName()
}

// ArgoProcess 一个入站端口对应一个 cloudflared 隧道进程
type ArgoProcess struct {
	cmd       *exec.Cmd
	tag       string
	port      int
	protocol  string
	domain    string // 解析出的 trycloudflare.com 域名
	startTime time.Time
	exitErr   error
	mu        sync.Mutex
}

func NewArgoProcess(tag string, port int, protocol string) *ArgoProcess {
	return &ArgoProcess{tag: tag, port: port, protocol: protocol, startTime: time.Now()}
}

func (p *ArgoProcess) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	return p.cmd.ProcessState == nil
}

func (p *ArgoProcess) GetDomain() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.domain
}

func (p *ArgoProcess) setDomain(d string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.domain = d
}

func (p *ArgoProcess) GetErr() error {
	return p.exitErr
}

func (p *ArgoProcess) GetPort() int {
	return p.port
}

func (p *ArgoProcess) GetTag() string {
	return p.tag
}

func (p *ArgoProcess) GetProtocol() string {
	return p.protocol
}

// Start 启动 cloudflared 临时隧道: cloudflared tunnel --url http://127.0.0.1:<port>
func (p *ArgoProcess) Start() (err error) {
	if p.IsRunning() {
		return errors.New("argo tunnel is already running")
	}
	defer func() {
		if err != nil {
			logger.Error("Failure in running cloudflared process: ", err)
			p.exitErr = err
		}
	}()

	err = os.MkdirAll(config.GetLogFolder(), 0o770)
	if err != nil {
		logger.Warningf("Failed to create log folder: %s", err)
	}

	logPath := fmt.Sprintf("%s/argo_%d.log", config.GetLogFolder(), p.port)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return common.NewErrorf("Failed to create argo log file: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", p.port)
	cmd := exec.Command(GetBinaryPath(), "tunnel", "--url", url,
		"--no-autoupdate", "--edge-ip-version", "auto", "--protocol", "http2")
	p.cmd = cmd
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	go func() {
		err := cmd.Run()
		if err != nil {
			logger.Error("Failure in running cloudflared:", err)
			p.exitErr = err
		}
	}()

	go p.watchDomain(logPath)

	return nil
}

var tryCloudflareRegexp = regexp.MustCompile(`https://([a-z0-9-]+\.trycloudflare\.com)`)

// watchDomain 轮询日志解析 trycloudflare.com 域名
func (p *ArgoProcess) watchDomain(logPath string) {
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		if !p.IsRunning() {
			return
		}
		data, err := os.ReadFile(logPath)
		if err != nil {
			continue
		}
		m := tryCloudflareRegexp.FindSubmatch(data)
		if m != nil {
			p.setDomain(string(m[1]))
			logger.Info(fmt.Sprintf("Argo 隧道申请成功 [端口 %d]: %s", p.port, m[1]))
			return
		}
	}
}

func (p *ArgoProcess) Stop() error {
	if !p.IsRunning() {
		return errors.New("argo tunnel is not running")
	}
	if runtime.GOOS == "windows" {
		return p.cmd.Process.Kill()
	}
	return p.cmd.Process.Signal(syscall.SIGTERM)
}
