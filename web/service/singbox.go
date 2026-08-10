package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/atomic"

	"x-ui/config"
	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/singbox"
	"x-ui/xray"
	json_util "x-ui/util/json_util"

	statsService "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	sp                    *singbox.Process
	singboxLock           sync.Mutex
	isNeedSingBoxRestart  atomic.Bool
	isSingBoxManuallyStop atomic.Bool
	singboxResult         string
)

// SingBoxService manages the sing-box core process. Inbounds with
// protocol anytls/tuic/naive are rendered into singbox.json and served
// by this process; all other protocols keep running under xray.
type SingBoxService struct {
	inboundService InboundService
	settingService SettingService
}

func (s *SingBoxService) IsSingBoxRunning() bool {
	return sp != nil && sp.IsRunning()
}

func (s *SingBoxService) GetSingBoxErr() error {
	if sp == nil {
		return nil
	}
	return sp.GetErr()
}

func (s *SingBoxService) GetSingBoxResult() string {
	if singboxResult != "" {
		return singboxResult
	}
	if s.IsSingBoxRunning() {
		return ""
	}
	if sp == nil {
		return ""
	}
	singboxResult = sp.GetResult()
	return singboxResult
}

func (s *SingBoxService) GetSingBoxVersion() string {
	if sp == nil {
		return "Unknown"
	}
	v := sp.GetVersion()
	if v == "" || strings.Contains(strings.ToLower(v), "unknown") {
		// 自定义构建未注入版本号时 sing-box version 输出 "unknown\n\nEnvironment:", 统一为 Unknown 以便前端隐藏版本 tag
		return "Unknown"
	}
	return v
}

// GetSingBoxConfig renders the sing-box configuration from all enabled
// inbounds whose protocol is served by the sing-box core.
func (s *SingBoxService) GetSingBoxConfig() (*singbox.Config, error) {
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}

	sbConfig := &singbox.Config{}
	logJSON, _ := json.Marshal(map[string]any{
		"level":     "info",
		"timestamp": true,
	})
	sbConfig.Log = json_util.RawMessage(logJSON)
	outboundJSON, _ := json.Marshal([]map[string]any{
		{"type": "direct", "tag": "direct"},
	})
	sbConfig.Outbounds = json_util.RawMessage(outboundJSON)

	// 流量统计：sing-box experimental.v2ray_api 提供与 xray 兼容的
	// StatsService，面板的 XrayTrafficJob 逻辑可复用。必须显式列出要
	// 统计的入站 tag 和用户 email，否则 sing-box 不产生任何统计。
	sbInboundTags := []string{}
	sbUserEmails := []string{}

	for _, inbound := range inbounds {
		if !model.IsSingBoxProtocol(inbound.Protocol) || !inbound.Enable {
			continue
		}
		sbInbound, buildErr := s.buildSingBoxInbound(inbound)
		if buildErr != nil {
			logger.Warningf("跳过 sing-box 入站 %s (%s): %v", inbound.Tag, inbound.Protocol, buildErr)
			continue
		}
		sbConfig.Inbounds = append(sbConfig.Inbounds, sbInbound)
		sbInboundTags = append(sbInboundTags, inbound.Tag)
		sbUserEmails = append(sbUserEmails, s.collectUserEmails(inbound)...)
	}

	// 注入 experimental.v2ray_api 统计配置（端口固定，与 xray api 端口错开）。
	if len(sbInboundTags) > 0 {
		expJSON, _ := json.Marshal(map[string]any{
			"v2ray_api": map[string]any{
				"listen": "127.0.0.1:62788",
				"stats": map[string]any{
					"enabled":  true,
					"inbounds": sbInboundTags,
					"users":    sbUserEmails,
				},
			},
		})
		sbConfig.Experimental = json_util.RawMessage(expJSON)
	}

	return sbConfig, nil
}

// SingBoxStatsAPIPort is the fixed listen port for sing-box's
// experimental.v2ray_api stats service (injected in GetSingBoxConfig).
// Deliberately different from the xray api port (62789) to avoid clashes.
const SingBoxStatsAPIPort = 62788

// GetSingBoxTraffic queries traffic counters from the running sing-box
// process via its experimental.v2ray_api StatsService. sing-box's gRPC
// service name is the legacy proto package "v2ray.core.app.stats.command.
// StatsService", whereas the panel's xray-core (v1.260327.0) client uses
// "xray.app.stats.command.StatsService" — the wire format is identical
// (same field numbers), so we invoke the legacy service name directly.
// Returns per-inbound and per-user (email) counters.
func (s *SingBoxService) GetSingBoxTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
	if !s.IsSingBoxRunning() {
		return nil, nil, errors.New("sing-box is not running")
	}
	conn, err := grpc.NewClient("127.0.0.1:62788", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Debug("Failed to connect sing-box stats API:", err)
		return nil, nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &statsService.QueryStatsRequest{Reset_: true}
	resp := new(statsService.QueryStatsResponse)
	if err := conn.Invoke(ctx, "/v2ray.core.app.stats.command.StatsService/QueryStats", req, resp); err != nil {
		logger.Debug("Failed to query sing-box stats:", err)
		return nil, nil, err
	}
	inboundTraffics, clientTraffics := xray.ParseTraffic(resp.GetStat())
	return inboundTraffics, clientTraffics, nil
}

// buildSingBoxInbound converts one q-ui inbound row into a sing-box
// inbound map. streamSettings.security drives the tls block:
//   - "tls"     -> standard TLS (anytls/tuic/naive)
//   - "reality" -> anytls only (Any-Reality)
//   - "" / none -> anytls only (plain, for testing)
func (s *SingBoxService) buildSingBoxInbound(inbound *model.Inbound) (map[string]any, error) {
	listen := inbound.Listen
	if listen == "" {
		listen = "::"
	}

	sb := map[string]any{
		"type":        string(inbound.Protocol),
		"tag":         inbound.Tag,
		"listen":      listen,
		"listen_port": inbound.Port,
	}

	users, err := s.buildSingBoxUsers(inbound)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, errors.New("没有可用的客户端")
	}
	sb["users"] = users

	switch inbound.Protocol {
	case model.AnyTLS:
		sb["padding_scheme"] = []string{}
	case model.Tuic:
		sb["congestion_control"] = extractSettingString(inbound.Settings, "congestionControl", "bbr")
	}

	tlsBlock, err := s.buildSingBoxTLS(inbound)
	if err != nil {
		return nil, err
	}
	if tlsBlock != nil {
		sb["tls"] = tlsBlock
	}

	return sb, nil
}

// buildSingBoxUsers extracts active clients from inbound.Settings and
// maps them to the sing-box user schema of each protocol.
func (s *SingBoxService) buildSingBoxUsers(inbound *model.Inbound) ([]map[string]any, error) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return nil, fmt.Errorf("解析 settings 失败: %v", err)
	}

	rawClients, ok := settings["clients"].([]any)
	if !ok {
		return nil, errors.New("settings 中缺少 clients")
	}

	disabledByStat := map[string]bool{}
	for _, stat := range inbound.ClientStats {
		if !stat.Enable {
			disabledByStat[stat.Email] = true
		}
	}

	var users []map[string]any
	for _, raw := range rawClients {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if en, ok := c["enable"].(bool); ok && !en {
			continue
		}
		email, _ := c["email"].(string)
		if disabledByStat[email] {
			continue
		}

		switch inbound.Protocol {
		case model.AnyTLS:
			password, _ := c["password"].(string)
			if password == "" {
				continue
			}
			u := map[string]any{"password": password}
			if email != "" {
				u["name"] = email
			}
			users = append(users, u)
		case model.Tuic:
			id, _ := c["id"].(string)
			password, _ := c["password"].(string)
			if id == "" {
				continue
			}
			u := map[string]any{"uuid": id, "password": password}
			if email != "" {
				u["name"] = email
			}
			users = append(users, u)
		case model.Naive:
			username, _ := c["username"].(string)
			password, _ := c["password"].(string)
			if username == "" {
				continue
			}
			users = append(users, map[string]any{"username": username, "password": password})
		}
	}
	return users, nil
}

// collectUserEmails returns the emails (sing-box user name fields) of all
// active clients of a sing-box inbound. These names are what sing-box's
// v2ray_api stats reports as `user>>>{name}>>>traffic>>>...`, matching the
// panel's client traffic rows keyed by email.
func (s *SingBoxService) collectUserEmails(inbound *model.Inbound) []string {
	var settings map[string]any
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return nil
	}
	rawClients, ok := settings["clients"].([]any)
	if !ok {
		return nil
	}
	disabledByStat := map[string]bool{}
	for _, stat := range inbound.ClientStats {
		if !stat.Enable {
			disabledByStat[stat.Email] = true
		}
	}
	emails := []string{}
	seen := map[string]bool{}
	for _, raw := range rawClients {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if en, ok := c["enable"].(bool); ok && !en {
			continue
		}
		email, _ := c["email"].(string)
		if email == "" || disabledByStat[email] || seen[email] {
			continue
		}
		seen[email] = true
		emails = append(emails, email)
	}
	return emails
}

// buildSingBoxTLS builds the sing-box tls block from streamSettings.
func (s *SingBoxService) buildSingBoxTLS(inbound *model.Inbound) (map[string]any, error) {
	var stream map[string]any
	if inbound.StreamSettings != "" {
		if err := json.Unmarshal([]byte(inbound.StreamSettings), &stream); err != nil {
			return nil, fmt.Errorf("解析 streamSettings 失败: %v", err)
		}
	}

	security, _ := stream["security"].(string)
	if security == "" {
		security = "none"
	}

	requiresTLS := inbound.Protocol == model.Tuic || inbound.Protocol == model.Naive
	if security == "none" {
		if requiresTLS {
			return nil, errors.New("Tuic/Naive 必须启用 TLS，请在传输设置中选择 security=TLS")
		}
		return nil, nil // anytls plain
	}

	tlsBlock := map[string]any{"enabled": true}

	if security == "reality" {
		if inbound.Protocol != model.AnyTLS {
			return nil, errors.New("Reality 仅支持 AnyTLS (Any-Reality)")
		}
		reality, err := buildRealityBlock(stream)
		if err != nil {
			return nil, err
		}
		tlsBlock["reality"] = reality
	}

	// server_name (reality 入站可无 tlsSettings 块, 必须独立处理)
	tlsSettings, _ := stream["tlsSettings"].(map[string]any)
	sni := ""
	if tlsSettings != nil {
		sni, _ = tlsSettings["sni"].(string)
	}
	if sni == "" && security == "reality" {
		// reality 的 server_name 优先从 realmSettings.serverNames 取
		if rs, ok2 := stream["realitySettings"].(map[string]any); ok2 {
			if names, ok3 := rs["serverNames"].([]any); ok3 && len(names) > 0 {
				if name, ok4 := names[0].(string); ok4 && name != "" {
					sni = name
				}
			}
		}
	}
	if sni != "" {
		tlsBlock["server_name"] = sni
	}
	// alpn / certificates
	if tlsSettings != nil {
		if alpn, ok := tlsSettings["alpn"].([]any); ok && len(alpn) > 0 {
			var alpnList []string
			for _, a := range alpn {
				if str, ok := a.(string); ok && str != "" {
					alpnList = append(alpnList, str)
				}
			}
			if len(alpnList) > 0 {
				tlsBlock["alpn"] = alpnList
			}
		}
		// certificates
		if certs, ok := tlsSettings["certificates"].([]any); ok && len(certs) > 0 {
			certPath, keyPath, certErr := resolveCertPaths(inbound, certs[0])
			if certErr != nil {
				return nil, certErr
			}
			tlsBlock["certificate_path"] = certPath
			tlsBlock["key_path"] = keyPath
		}
	}

	if _, hasCert := tlsBlock["certificate_path"]; !hasCert && requiresTLS {
		return nil, errors.New("Tuic/Naive 需要证书，请在 TLS 设置中添加证书（路径或内容）")
	}

	return tlsBlock, nil
}

// buildRealityBlock maps q-ui realitySettings onto the sing-box
// reality schema.
func buildRealityBlock(stream map[string]any) (map[string]any, error) {
	realitySettings, ok := stream["realitySettings"].(map[string]any)
	if !ok {
		return nil, errors.New("缺少 realitySettings")
	}
	privateKey, _ := realitySettings["privateKey"].(string)
	if privateKey == "" {
		return nil, errors.New("缺少 Reality privateKey")
	}

	reality := map[string]any{
		"enabled":     true,
		"private_key": privateKey,
	}

	// target "host:port" -> handshake
	target, _ := realitySettings["target"].(string)
	if target == "" {
		return nil, errors.New("缺少 Reality target (handshake 地址)")
	}
	host, port := splitHostPort(target)
	handshake := map[string]any{"server": host}
	if port > 0 {
		handshake["server_port"] = port
	}
	reality["handshake"] = handshake

	// shortIds -> ["a","b"] (兼容数组或逗号分隔字符串)
	var ids []string
	if shortIdsArr, ok := realitySettings["shortIds"].([]any); ok {
		for _, id := range shortIdsArr {
			if s, ok := id.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					ids = append(ids, s)
				}
			}
		}
	} else if shortIdsStr, ok := realitySettings["shortIds"].(string); ok && shortIdsStr != "" {
		for _, id := range strings.Split(shortIdsStr, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) > 0 {
		reality["short_id"] = ids
	}



	return reality, nil
}

// resolveCertPaths returns certificate/key paths for the sing-box tls
// block. Path-based certs are used as-is; content-based certs are
// written under the bin folder keyed by the inbound tag.
func resolveCertPaths(inbound *model.Inbound, certRaw any) (string, string, error) {
	cert, ok := certRaw.(map[string]any)
	if !ok {
		return "", "", errors.New("证书格式无效")
	}

	if certFile, ok := cert["certificateFile"].(string); ok && certFile != "" {
		keyFile, _ := cert["keyFile"].(string)
		if keyFile == "" {
			return "", "", errors.New("证书路径模式缺少 keyFile")
		}
		return certFile, keyFile, nil
	}

	certContent, _ := cert["certificate"].([]any)
	keyContent, _ := cert["key"].([]any)
	if len(certContent) == 0 || len(keyContent) == 0 {
		return "", "", errors.New("证书内容为空")
	}

	tag := sanitizeTag(inbound.Tag)
	certPath := filepath.Join(config.GetBinFolderPath(), "singbox_"+tag+".crt")
	keyPath := filepath.Join(config.GetBinFolderPath(), "singbox_"+tag+".key")

	if err := os.WriteFile(certPath, []byte(strings.Join(stringifySlice(certContent), "\n")), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, []byte(strings.Join(stringifySlice(keyContent), "\n")), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func (s *SingBoxService) RestartSingBox(isForce bool) error {
	singboxLock.Lock()
	defer singboxLock.Unlock()
	logger.Debug("restart sing-box, force:", isForce)
	isSingBoxManuallyStop.Store(false)

	sbConfig, err := s.GetSingBoxConfig()
	if err != nil {
		return err
	}

	// No sing-box inbounds -> make sure the process is stopped.
	if len(sbConfig.Inbounds) == 0 {
		if s.IsSingBoxRunning() {
			logger.Info("没有 sing-box 协议入站，停止 sing-box 进程")
			if err := sp.Stop(); err != nil {
				logger.Warning("stop sing-box failed:", err)
			}
		}
		sp = nil
		singboxResult = ""
		return nil
	}

	if s.IsSingBoxRunning() {
		if !isForce && sp.GetConfig().Equals(sbConfig) && !isNeedSingBoxRestart.Load() {
			logger.Debug("sing-box 配置未变化，无需重启")
			return nil
		}
		if err := sp.Stop(); err != nil {
			logger.Warning("stop sing-box failed:", err)
		}
	}

	sp = singbox.NewProcess(sbConfig)
	singboxResult = ""
	if err := sp.Start(); err != nil {
		return err
	}
	return nil
}

func (s *SingBoxService) StopSingBox() error {
	singboxLock.Lock()
	defer singboxLock.Unlock()
	isSingBoxManuallyStop.Store(true)
	logger.Debug("Attempting to stop sing-box...")
	if s.IsSingBoxRunning() {
		return sp.Stop()
	}
	return errors.New("sing-box is not running")
}

func (s *SingBoxService) SetToNeedRestart() {
	isNeedSingBoxRestart.Store(true)
}

// MarkSingBoxNeedRestart 包级入口：InboundService 等零值实例也可直接标记，
// 与 web.go 中 SingBoxService 实例读取的是同一个包级标志。
func MarkSingBoxNeedRestart() {
	isNeedSingBoxRestart.Store(true)
}

func (s *SingBoxService) IsNeedRestartAndSetFalse() bool {
	return isNeedSingBoxRestart.CompareAndSwap(true, false)
}

// DidSingBoxCrash reports whether the sing-box process is down without
// a manual stop, i.e. it crashed or exited unexpectedly.
func (s *SingBoxService) DidSingBoxCrash() bool {
	return !s.IsSingBoxRunning() && !isSingBoxManuallyStop.Load()
}

// ---------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------

func extractSettingString(settingsJSON, key, def string) string {
	var settings map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return def
	}
	if v, ok := settings[key].(string); ok && v != "" {
		return v
	}
	return def
}

func splitHostPort(target string) (string, int) {
	if idx := strings.LastIndex(target, ":"); idx > 0 {
		host := target[:idx]
		port := 0
		fmt.Sscanf(target[idx+1:], "%d", &port)
		return host, port
	}
	return target, 0
}

func sanitizeTag(tag string) string {
	replacer := strings.NewReplacer(":", "_", "/", "_", ".", "_", "-", "_", " ", "_")
	return replacer.Replace(tag)
}

func stringifySlice(s []any) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}
