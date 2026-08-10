package service

import (
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/atomic"

	"x-ui/argo"
	"x-ui/database/model"
	"x-ui/logger"
)

var (
	argoLock         sync.Mutex
	argoProcesses    = make(map[string]*argo.ArgoProcess) // tag -> process
	argoNeedRestart  atomic.Bool
	argoManuallyStop atomic.Bool
)

// ArgoService manages cloudflared quick-tunnel processes. Any enabled
// vmess/vless inbound whose streamSettings.argoSettings.enabled is true
// gets its own cloudflared process mapping http://127.0.0.1:<port> to a
// trycloudflare.com free domain.
type ArgoService struct {
	inboundService InboundService
}

// ArgoStatus 面板展示的隧道状态
type ArgoStatus struct {
	Tag      string `json:"tag"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Domain   string `json:"domain"`
	Running  bool   `json:"running"`
}

func NewArgoService(inboundService InboundService) *ArgoService {
	return &ArgoService{inboundService: inboundService}
}

// IsArgoRunning reports whether any argo tunnel process is alive.
func (s *ArgoService) IsArgoRunning() bool {
	argoLock.Lock()
	defer argoLock.Unlock()
	return len(argoProcesses) > 0
}

// GetArgoDomain returns the tunnel domain for the given inbound tag.
func (s *ArgoService) GetArgoDomain(tag string) string {
	argoLock.Lock()
	defer argoLock.Unlock()
	if p, ok := argoProcesses[tag]; ok {
		return p.GetDomain()
	}
	return ""
}

// GetArgoStatuses returns tag -> status for every tunnel.
func (s *ArgoService) GetArgoStatuses() []ArgoStatus {
	argoLock.Lock()
	defer argoLock.Unlock()
	statuses := make([]ArgoStatus, 0, len(argoProcesses))
	for tag, p := range argoProcesses {
		statuses = append(statuses, ArgoStatus{
			Tag:      tag,
			Port:     p.GetPort(),
			Protocol: p.GetProtocol(),
			Domain:   p.GetDomain(),
			Running:  p.IsRunning(),
		})
	}
	return statuses
}

// shouldEnableArgo 判断入站是否启用 Argo 隧道:
// vmess/vless + enable + network=ws + argoSettings.enabled=true
func shouldEnableArgo(inbound *model.Inbound) bool {
	if !inbound.Enable || (inbound.Protocol != "vmess" && inbound.Protocol != "vless") {
		return false
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(inbound.StreamSettings), &stream); err != nil {
		return false
	}
	network, _ := stream["network"].(string)
	if network != "ws" {
		return false
	}
	argoSettings, ok := stream["argo"].(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := argoSettings["enabled"].(bool)
	return enabled
}

// RestartArgo stops every existing tunnel and starts one per eligible inbound.
func (s *ArgoService) RestartArgo() error {
	argoLock.Lock()
	defer argoLock.Unlock()
	logger.Debug("restart argo")
	argoManuallyStop.Store(false)

	for tag, p := range argoProcesses {
		if p.IsRunning() {
			if err := p.Stop(); err != nil {
				logger.Warning("stop argo tunnel failed:", err)
			}
		}
		delete(argoProcesses, tag)
	}

	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return err
	}
	started := 0
	for _, inbound := range inbounds {
		if !shouldEnableArgo(inbound) {
			continue
		}
		p := argo.NewArgoProcess(inbound.Tag, inbound.Port, string(inbound.Protocol))
		if err := p.Start(); err != nil {
			logger.Warning("start argo tunnel failed, tag:", inbound.Tag, err)
			continue
		}
		argoProcesses[inbound.Tag] = p
		started++
	}
	logger.Info(fmt.Sprintf("Argo 隧道重启完成，共启动 %d 个隧道", started))
	return nil
}

// StopArgo stops all tunnel processes.
func (s *ArgoService) StopArgo() error {
	argoLock.Lock()
	defer argoLock.Unlock()
	argoManuallyStop.Store(true)
	for tag, p := range argoProcesses {
		if p.IsRunning() {
			if err := p.Stop(); err != nil {
				logger.Warning("stop argo tunnel failed:", err)
			}
		}
		delete(argoProcesses, tag)
	}
	return nil
}

func (s *ArgoService) SetToNeedRestart() {
	argoNeedRestart.Store(true)
}

// MarkArgoNeedRestart 包级入口：InboundService 等零值实例也可直接标记
func MarkArgoNeedRestart() {
	argoNeedRestart.Store(true)
}

func (s *ArgoService) IsNeedRestartAndSetFalse() bool {
	return argoNeedRestart.CompareAndSwap(true, false)
}

// DidArgoCrash reports whether a tunnel process died unexpectedly.
func (s *ArgoService) DidArgoCrash() bool {
	argoLock.Lock()
	defer argoLock.Unlock()
	if len(argoProcesses) == 0 {
		return false
	}
	for _, p := range argoProcesses {
		if !p.IsRunning() {
			return true
		}
	}
	return false
}
