package service

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"x-ui/logger"
)

// ---------------------------------------------------------------------------
// 出站显式接管入站（跨引擎路由绑定）共享数据结构与工具。
//
// 数据模型（settings 表两个新键）：
//   outboundRoutes  : JSON 数组，每项 {engine, outbound, inbounds:[{engine, tag}]}
//                     engine 指出站所在内核（xray|singbox），inbounds 是被接管
//                     的入站（各自引擎 + tag）。
//   bridgePortAlloc : JSON map，key = "<dirKey>", value = 回环桥端口。
//                     跨引擎桥一律监听/连接 127.0.0.1：
//                       dirKey "x2sb:<ob>" = xray入站 -> singbox出站
//                       dirKey "sb2x:<ob>" = singbox入站 -> xray出站
// ---------------------------------------------------------------------------

type routeInboundRef struct {
	Engine string `json:"engine"`
	Tag    string `json:"tag"`
}

type outboundRouteItem struct {
	Engine   string            `json:"engine"`
	Outbound string            `json:"outbound"`
	Inbounds []routeInboundRef `json:"inbounds"`
}

const bridgePortBase = 20000

var bridgeAllocMu sync.Mutex

// readOutboundRoutes 读取 outboundRoutes 设置键并解析为结构体切片。
func readOutboundRoutes(settingSvc SettingService) []outboundRouteItem {
	raw, err := settingSvc.GetString("outboundRoutes")
	if err != nil || raw == "" {
		return nil
	}
	var items []outboundRouteItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		logger.Warningf("outboundRoutes 解析失败（已忽略）: %v", err)
		return nil
	}
	return items
}

// readBridgeAlloc 读取 bridgePortAlloc 设置键（map：方向键 -> 端口）。
func readBridgeAlloc(settingSvc SettingService) map[string]int {
	ports := map[string]int{}
	raw, err := settingSvc.GetString("bridgePortAlloc")
	if err != nil || raw == "" {
		return ports
	}
	if err := json.Unmarshal([]byte(raw), &ports); err != nil {
		logger.Warningf("bridgePortAlloc 解析失败（按空表处理）: %v", err)
		return map[string]int{}
	}
	return ports
}

// allocBridgePort 返回 dirKey 对应的回环桥端口；不存在则从 20000 起找第一个
// 未占用且未分配过的端口，写回 bridgePortAlloc 后返回。幂等：同一 dirKey 在
// 任意内核侧调用都得到相同端口。mutex 串行化读改写，避免双内核并发竞态。
func allocBridgePort(settingSvc SettingService, dirKey string) int {
	bridgeAllocMu.Lock()
	defer bridgeAllocMu.Unlock()

	ports := readBridgeAlloc(settingSvc)
	if v, ok := ports[dirKey]; ok && v > 0 {
		return v
	}
	used := map[int]bool{}
	for _, v := range ports {
		used[v] = true
	}
	port := 0
	for p := bridgePortBase; p < bridgePortBase+10000; p++ {
		if used[p] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		port = p
		break
	}
	if port == 0 {
		logger.Warningf("无法为桥接 %s 分配空闲端口（%d-%d 已用尽）", dirKey, bridgePortBase, bridgePortBase+9999)
		return 0
	}
	ports[dirKey] = port
	buf, _ := json.Marshal(ports)
	if err := settingSvc.SetString("bridgePortAlloc", string(buf)); err != nil {
		logger.Warningf("保存 bridgePortAlloc 失败: %v", err)
	}
	logger.Infof("分配跨引擎回环桥端口 %s -> 127.0.0.1:%d", dirKey, port)
	return port
}

// isSelfAddr 判断主机地址是否指向本机（防回环铁律：任何入站不得被导到
// 目标为本机的出站）。识别 127.0.0.1/::1/localhost/0.0.0.0/:: 及本机网卡 IP。
func isSelfAddr(addr string) bool {
	if addr == "" {
		return false
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	switch host {
	case "127.0.0.1", "::1", "localhost", "0.0.0.0", "::", "[::1]", "[::]":
		return true
	}
	if ips, err := net.InterfaceAddrs(); err == nil {
		for _, ip := range ips {
			if ipNet, ok := ip.(*net.IPNet); ok && ipNet.IP.String() == host {
				return true
			}
		}
	}
	return false
}

// xrayOutboundServerAddr 从 xray 格式出站 map 提取目标服务器地址，用于自指检测。
func xrayOutboundServerAddr(ob map[string]any) string {
	if ob == nil {
		return ""
	}
	if sett, ok := ob["settings"].(map[string]any); ok {
		if vnext, ok := sett["vnext"].([]any); ok && len(vnext) > 0 {
			if first, ok := vnext[0].(map[string]any); ok {
				if a, _ := first["address"].(string); a != "" {
					return a
				}
			}
		}
		if srv, ok := sett["servers"].([]any); ok && len(srv) > 0 {
			if first, ok := srv[0].(map[string]any); ok {
				if a, _ := first["address"].(string); a != "" {
					return a
				}
			}
		}
		if a, _ := sett["address"].(string); a != "" {
			return a
		}
	}
	if a, _ := ob["address"].(string); a != "" {
		return a
	}
	return ""
}

// loadSingboxUserOutbounds 读取 singboxOutboundTemplate（JSON 数组）为出站 map 列表。
func loadSingboxUserOutbounds(settingSvc SettingService) []map[string]any {
	tpl, err := settingSvc.GetString("singboxOutboundTemplate")
	if err != nil || tpl == "" || tpl == "[]" {
		return nil
	}
	var arr []map[string]any
	if json.Unmarshal([]byte(tpl), &arr) != nil {
		return nil
	}
	return arr
}

// xrayTemplateOutboundMap 解析 xrayTemplateConfig 的 outbounds，
// 返回 map[tag] = 出站 map（含 tag/protocol/settings…）。
func xrayTemplateOutboundMap(settingSvc SettingService) map[string]map[string]any {
	tpl, err := settingSvc.GetXrayConfigTemplate()
	if err != nil || tpl == "" {
		return nil
	}
	var cfg map[string]any
	if json.Unmarshal([]byte(tpl), &cfg) != nil {
		return nil
	}
	obs, _ := cfg["outbounds"].([]any)
	out := map[string]map[string]any{}
	for _, obRaw := range obs {
		ob, ok := obRaw.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := ob["tag"].(string)
		if tag != "" {
			out[tag] = ob
		}
	}
	return out
}

// scrubOutboundRoutes 兜底清理：删除 outboundRoutes 中 engine 内核下已不存在的
// 出站条目并写回 settings（出站被删除后同步清掉绑定）。
func scrubOutboundRoutes(settingSvc SettingService, engine string, validTags map[string]bool) {
	routes := readOutboundRoutes(settingSvc)
	if len(routes) == 0 {
		return
	}
	changed := false
	kept := make([]outboundRouteItem, 0, len(routes))
	for _, rt := range routes {
		if rt.Engine == engine && !validTags[rt.Outbound] {
			logger.Warningf("出站 %s 已不存在，清理其接管路由配置", rt.Outbound)
			changed = true
			continue
		}
		kept = append(kept, rt)
	}
	if !changed {
		return
	}
	buf, _ := json.Marshal(kept)
	_ = settingSvc.SetString("outboundRoutes", string(buf))
}
