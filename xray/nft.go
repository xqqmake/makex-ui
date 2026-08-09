package xray

import (
	"encoding/json"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// HysteriaPortHoppingRule 描述一条 hysteria 端口跳跃 DNAT 规则。
type HysteriaPortHoppingRule struct {
	Ports    string // 如 "30000-45000" 或 "30000-30050,30100"
	MainPort int    // 服务端主端口
}

// ExtractHysteriaPortHoppingRules 从 xray 配置中提取 hysteria 端口跳跃规则。
// 跳跃配置位于 inbound.streamSettings.hysteria.portHopping.{enabled,ports}。
func ExtractHysteriaPortHoppingRules(cfg *Config) []HysteriaPortHoppingRule {
	var rules []HysteriaPortHoppingRule
	for _, in := range cfg.InboundConfigs {
		if in.Protocol != "hysteria" {
			continue
		}
		var stream map[string]any
		if err := json.Unmarshal(in.StreamSettings, &stream); err != nil {
			continue
		}
		hysteria, ok := stream["hysteria"].(map[string]any)
		if !ok {
			continue
		}
		ph, ok := hysteria["portHopping"].(map[string]any)
		if !ok {
			continue
		}
		enabled, _ := ph["enabled"].(bool)
		if !enabled {
			continue
		}
		ports, _ := ph["ports"].(string)
		if strings.TrimSpace(ports) == "" {
			continue
		}
		// 主端口: in.Port 是 RawMessage(纯数字)
		var mainPort int
		if err := json.Unmarshal(in.Port, &mainPort); err != nil {
			continue
		}
		rules = append(rules, HysteriaPortHoppingRule{Ports: ports, MainPort: mainPort})
	}
	return rules
}

// ApplyHysteriaPortHoppingRules 用 nft(优先)/iptables 把跳跃范围 DNAT/REDIRECT 到主端口。
// 服务端 xray 只监听主端口，跳跃范围内的 UDP 包由防火墙转发到主端口，避免 xray
// 为每个端口各开 socket 导致 OOM。使用独立表 q-ui-hopping，每次全量重建保证幂等。
// 防火墙工具不可用时返回 0,nil（不阻塞 xray 启动）。
func ApplyHysteriaPortHoppingRules(cfg *Config) (int, error) {
	rules := ExtractHysteriaPortHoppingRules(cfg)
	return ApplyRules(rules)
}

// ApplyRules 全量重建 hysteria 端口跳跃 DNAT 规则（从任意规则来源，如 DB）。
// 无规则时返回 0,nil；防火墙工具不可用时返回 0,nil（不阻塞调用方）。
func ApplyRules(rules []HysteriaPortHoppingRule) (int, error) {
	if len(rules) == 0 {
		return 0, nil
	}
	bin := ""
	for _, cand := range []string{"nft", "iptables"} {
		if p, err := exec.LookPath(cand); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		log.Printf("[xray] no nft/iptables found, skip hysteria port hopping rules")
		return 0, nil
	}
	if strings.HasSuffix(bin, "nft") {
		return applyNFT(bin, rules)
	}
	return applyIPTables(bin, rules)
}

func applyNFT(bin string, rules []HysteriaPortHoppingRule) (int, error) {
	// 建表/链(已存在则忽略错误)
	exec.Command(bin, "add", "table", "ip", "q-ui-hopping").Run()
	exec.Command(bin, "add", "chain", "ip", "q-ui-hopping", "prerouting",
		"{ type nat hook prerouting priority dstnat; }").Run()
	// 全量重建
	exec.Command(bin, "flush", "chain", "ip", "q-ui-hopping", "prerouting").Run()
	n := 0
	for _, r := range rules {
		for _, one := range strings.Split(r.Ports, ",") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			cmd := exec.Command(bin, "add", "rule", "ip", "q-ui-hopping", "prerouting",
				"udp", "dport", one, "redirect", "to", ":"+strconv.Itoa(r.MainPort))
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("[xray] nft add rule failed (%s -> %d): %s", one, r.MainPort, string(out))
				continue
			}
			n++
		}
	}
	return n, nil
}

func applyIPTables(bin string, rules []HysteriaPortHoppingRule) (int, error) {
	// 清旧规则
	for _, r := range rules {
		for _, one := range strings.Split(r.Ports, ",") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			exec.Command(bin, "-t", "nat", "-D", "PREROUTING", "-p", "udp",
				"--dport", one, "-j", "REDIRECT", "--to-ports", strconv.Itoa(r.MainPort)).Run()
		}
	}
	n := 0
	for _, r := range rules {
		for _, one := range strings.Split(r.Ports, ",") {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			if err := exec.Command(bin, "-t", "nat", "-A", "PREROUTING", "-p", "udp",
				"--dport", one, "-j", "REDIRECT", "--to-ports", strconv.Itoa(r.MainPort)).Run(); err == nil {
				n++
			}
		}
	}
	return n, nil
}
