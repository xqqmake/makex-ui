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
	g.GET("/getXrayResult", a.getXrayResult)
	g.GET("/getDefaultJsonConfig", a.getDefaultXrayConfig)
	g.POST("/warp/:action", a.warp)
	g.GET("/getOutboundsTraffic", a.getOutboundsTraffic)
	g.POST("/resetOutboundsTraffic", a.resetOutboundsTraffic)
	g.POST("/testOutbound", a.testOutbound)
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
	xrayResponse := "{ \"xraySetting\": " + xraySetting + ", \"inboundTags\": " + inboundTags + " }"
	jsonObj(c, xrayResponse, nil)
}

func (a *XraySettingController) updateSetting(c *gin.Context) {
	xraySetting := c.PostForm("xraySetting")
	err := a.XraySettingService.SaveXraySetting(xraySetting)
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
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
