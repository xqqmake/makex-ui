package controller

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"x-ui/web/service"
	"x-ui/xray"

	"github.com/gin-gonic/gin"
)

type XraySettingController struct {
	XraySettingService service.XraySettingService
	SettingService     service.SettingService
	InboundService     service.InboundService
	OutboundService    service.OutboundService
	XrayService        service.XrayService
	WarpService        service.WarpService
	SingBoxService     service.SingBoxService
}

func NewXraySettingController(g *gin.RouterGroup) *XraySettingController {
	a := &XraySettingController{}
	a.initRouter(g)
	return a
}

func (a *XraySettingController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/xray")

	g.POST("/", a.getXraySetting)
	g.POST("/update", a.updateSetting)
	g.POST("/updateSingboxOutbounds", a.updateSingboxOutbounds)
	g.GET("/getXrayResult", a.getXrayResult)
	g.GET("/getDefaultJsonConfig", a.getDefaultXrayConfig)
	g.POST("/warp/:action", a.warp)
	g.GET("/getOutboundsTraffic", a.getOutboundsTraffic)
	g.POST("/resetOutboundsTraffic", a.resetOutboundsTraffic)
	g.POST("/testOutbound", a.testOutbound)
	g.POST("/importOutbounds", a.importOutbounds)
}

// importOutbounds 一键导入出站：解析分享链接，生成标准出站 JSON 并追加到对应模板，
// 保存后自动重启生效。xray 协议（vless/vmess/trojan/ss/socks/hysteria/hysteria2）写 xray 模板；
// sing-box 协议（anytls/tuic/naive）写 singbox 出站模板（singboxOutboundTemplate）。
func (a *XraySettingController) importOutbounds(c *gin.Context) {
	links := c.PostForm("links")
	if links == "" {
		jsonMsg(c, "links is required", nil)
		return
	}

	// 读取当前 xray 模板并解析 outbounds
	tmpl, err := a.SettingService.GetXrayConfigTemplate()
	if err != nil {
		jsonMsg(c, "读取 xray 模板失败: "+err.Error(), nil)
		return
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(tmpl), &cfg); err != nil {
		jsonMsg(c, "xray 模板解析失败: "+err.Error(), nil)
		return
	}
	var outbounds []any
	if ob, ok := cfg["outbounds"].([]any); ok {
		outbounds = ob
	}

	// 读取当前 sing-box 出站模板（JSON 数组）
	sbTpl, _ := a.SettingService.GetString("singboxOutboundTemplate")
	var sbOutbounds []map[string]any
	if sbTpl != "" {
		if err := json.Unmarshal([]byte(sbTpl), &sbOutbounds); err != nil {
			sbOutbounds = nil
		}
	}

	usedTags := map[string]bool{}
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]any); ok {
			if tag, ok := m["tag"].(string); ok && tag != "" {
				usedTags[tag] = true
			}
		}
	}
	for _, ob := range sbOutbounds {
		if tag, ok := ob["tag"].(string); ok && tag != "" {
			usedTags[tag] = true
		}
	}

	isSingBoxProtocol := func(proto string) bool {
		switch proto {
		case "anytls", "tuic", "naive":
			return true
		}
		return false
	}

	created := []map[string]any{}
	failed := []string{}
	for _, line := range strings.Split(links, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, err := service.ParseShareLink(line)
		if err != nil {
			failed = append(failed, line+" → "+err.Error())
			continue
		}
		// 生成唯一 tag：优先用备注，冲突时加后缀
		base := p.Remark
		if base == "" {
			base = p.Address
		}
		tag := base
		i := 1
		for usedTags[tag] {
			i++
			tag = fmt.Sprintf("%s-%d", base, i)
		}
		usedTags[tag] = true

		if isSingBoxProtocol(p.Protocol) {
			// anytls/tuic/naive：由 sing-box 内核承载出站
			sbOutbound := p.BuildSingBoxOutbound(tag)
			sbOutbounds = append(sbOutbounds, sbOutbound)
		} else {
			outbound := p.BuildOutbound(tag)
			outbounds = append(outbounds, outbound)
		}
		created = append(created, map[string]any{
			"tag":      tag,
			"protocol": p.Protocol,
			"address":  p.Address,
			"port":     p.Port,
		})
	}

	if len(created) == 0 {
		jsonMsg(c, "没有成功解析任何链接", nil)
		return
	}

	xrayChanged := false
	if len(outbounds) > 0 {
		cfg["outbounds"] = outbounds
		newBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			jsonMsg(c, "序列化失败: "+err.Error(), nil)
			return
		}
		if err := a.XraySettingService.SaveXraySetting(string(newBytes)); err != nil {
			jsonMsg(c, "保存 xray 配置失败: "+err.Error(), nil)
			return
		}
		xrayChanged = true
	}
	singboxChanged := false
	if len(sbOutbounds) > 0 {
		sbBytes, _ := json.Marshal(sbOutbounds)
		if err := a.SettingService.SetString("singboxOutboundTemplate", string(sbBytes)); err != nil {
			jsonMsg(c, "保存 sing-box 出站模板失败: "+err.Error(), nil)
			return
		}
		singboxChanged = true
	}

	if xrayChanged {
		a.XrayService.SetToNeedRestart()
	}
	if singboxChanged {
		a.SingBoxService.SetToNeedRestart()
	}
	jsonObj(c, map[string]any{
		"success": true,
		"created": created,
		"failed":  failed,
	}, nil)
}

func (a *XraySettingController) getXraySetting(c *gin.Context) {
	xraySetting, err := a.SettingService.GetXrayConfigTemplate()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	inboundTags, err := a.InboundService.GetInboundTags()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	// 合并 sing-box 出站模板（anytls/tuic/naive 等 sing-box 协议出站），供出站列表统一展示
	resp := map[string]any{
		"xraySetting":     json.RawMessage(xraySetting),
		"inboundTags":     json.RawMessage(inboundTags),
		"singboxOutbounds": json.RawMessage("[]"),
	}
	if sbTpl, err := a.SettingService.GetString("singboxOutboundTemplate"); err == nil && sbTpl != "" && sbTpl != "[]" {
		resp["singboxOutbounds"] = json.RawMessage(sbTpl)
	}
	respBytes, _ := json.Marshal(resp)
	jsonObj(c, string(respBytes), nil)
}

func (a *XraySettingController) updateSetting(c *gin.Context) {
	xraySetting := c.PostForm("xraySetting")
	err := a.XraySettingService.SaveXraySetting(xraySetting)
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
}

// updateSingboxOutbounds 保存 sing-box 出站模板（anytls/tuic/naive 等）
func (a *XraySettingController) updateSingboxOutbounds(c *gin.Context) {
	sbTpl := c.PostForm("singboxOutbounds")
	if sbTpl == "" {
		sbTpl = "[]"
	}
	// 校验是合法 JSON 数组
	var arr []map[string]any
	if err := json.Unmarshal([]byte(sbTpl), &arr); err != nil {
		jsonMsg(c, "sing-box 出站模板格式错误", err)
		return
	}
	if err := a.SettingService.SetString("singboxOutboundTemplate", sbTpl); err != nil {
		jsonMsg(c, "保存 sing-box 出站模板失败", err)
		return
	}
	a.SingBoxService.SetToNeedRestart()
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), nil)
}

func (a *XraySettingController) getDefaultXrayConfig(c *gin.Context) {
	defaultJsonConfig, err := a.SettingService.GetDefaultXrayConfig()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, defaultJsonConfig, nil)
}

func (a *XraySettingController) getXrayResult(c *gin.Context) {
	jsonObj(c, a.XrayService.GetXrayResult(), nil)
}

func (a *XraySettingController) warp(c *gin.Context) {
	action := c.Param("action")
	var resp string
	var err error
	switch action {
	case "data":
		resp, err = a.WarpService.GetWarpData()
	case "del":
		err = a.WarpService.DelWarpData()
	case "config":
		resp, err = a.WarpService.GetWarpConfig()
	case "reg":
		skey := c.PostForm("privateKey")
		pkey := c.PostForm("publicKey")
		resp, err = a.WarpService.RegWarp(skey, pkey)
	case "license":
		license := c.PostForm("license")
		resp, err = a.WarpService.SetWarpLicense(license)
	}

	jsonObj(c, resp, err)
}

func (a *XraySettingController) getOutboundsTraffic(c *gin.Context) {
	outboundsTraffic, err := a.OutboundService.GetOutboundsTraffic()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getOutboundTrafficError"), err)
		return
	}
	jsonObj(c, outboundsTraffic, nil)
}

func (a *XraySettingController) resetOutboundsTraffic(c *gin.Context) {
	tag := c.PostForm("tag")
	err := a.OutboundService.ResetOutboundTraffic(tag)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.resetOutboundTrafficError"), err)
		return
	}
	jsonObj(c, "", nil)
}

// testOutbound 通过临时 xray 实例 + socks5 入站实测出站节点连通性
func (a *XraySettingController) testOutbound(c *gin.Context) {
	outboundJSON := c.PostForm("outbound")
	if outboundJSON == "" {
		jsonMsg(c, "outbound is required", nil)
		return
	}

	var outbound map[string]interface{}
	if err := json.Unmarshal([]byte(outboundJSON), &outbound); err != nil {
		jsonMsg(c, "invalid outbound json: "+err.Error(), nil)
		return
	}
	protocol, _ := outbound["protocol"].(string)
	if protocol == "" {
		jsonMsg(c, "protocol is required", nil)
		return
	}

	// 随机本地 socks 端口
	n, err := rand.Int(rand.Reader, big.NewInt(900))
	if err != nil {
		jsonMsg(c, "rand failed: "+err.Error(), nil)
		return
	}
	port := 39010 + int(n.Int64())
	tmpFile := fmt.Sprintf("/tmp/outbound-test-%d.json", port)
	defer os.Remove(tmpFile)

	cfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "warning"},
		"inbounds": []map[string]interface{}{{
			"port":     port,
			"protocol": "socks",
			"settings": map[string]interface{}{"auth": "noauth", "udp": true},
			"listen":   "127.0.0.1",
		}},
		"outbounds": []interface{}{outbound},
	}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		jsonMsg(c, "marshal config failed: "+err.Error(), nil)
		return
	}
	if err := os.WriteFile(tmpFile, cfgBytes, 0644); err != nil {
		jsonMsg(c, "write config failed: "+err.Error(), nil)
		return
	}

	cmd := exec.Command(xray.GetBinaryPath(), "run", "-c", tmpFile)
	if err := cmd.Start(); err != nil {
		jsonMsg(c, "start xray failed: "+err.Error(), nil)
		return
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// 等待 socks 端口就绪（最长 4s）
	ready := false
	for i := 0; i < 40; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		jsonObj(c, map[string]interface{}{"success": false, "error": "xray 测试实例启动超时", "latency": 0}, nil)
		return
	}

	proxyURL, _ := url.Parse(fmt.Sprintf("socks5h://127.0.0.1:%d", port))
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	start := time.Now()
	resp, err := client.Get("https://www.gstatic.com/generate_204")
	latency := time.Since(start).Milliseconds()
	if err != nil {
		jsonObj(c, map[string]interface{}{"success": false, "error": err.Error(), "latency": latency}, nil)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	jsonObj(c, map[string]interface{}{
		"success": resp.StatusCode >= 200 && resp.StatusCode < 400,
		"status":  resp.StatusCode,
		"latency": latency,
	}, nil)
}
