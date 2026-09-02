package service

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseVlessReality(t *testing.T) {
	link := "vless://6e5a0a5b-1b3f-4f3a-9c0e-8f1d2e3a4b5c@1.2.3.4:443?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome&pbk=abc123&sid=6f1d2e&spx=%2F&flow=xtls-rprx-vision&type=tcp#香港节点"
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.Protocol != "vless" || p.Address != "1.2.3.4" || p.Port != 443 {
		t.Fatalf("basic fields wrong: %+v", p)
	}
	if p.Remark != "香港节点" {
		t.Fatalf("remark wrong: %q", p.Remark)
	}
	if p.Security != "reality" || p.SNI != "www.microsoft.com" || p.PublicKey != "abc123" || p.ShortID != "6f1d2e" || p.SpiderX != "/" {
		t.Fatalf("reality fields wrong: %+v", p)
	}
	if p.Flow != "xtls-rprx-vision" || p.Network != "tcp" {
		t.Fatalf("flow/network wrong: %+v", p)
	}

	ob := p.BuildOutbound("test-tag")
	ss := ob["streamSettings"].(map[string]any)
	if ss["security"] != "reality" {
		t.Fatalf("stream security wrong: %v", ss)
	}
	rs := ss["realitySettings"].(map[string]any)
	if rs["publicKey"] != "abc123" || rs["shortId"] != "6f1d2e" {
		t.Fatalf("realitySettings wrong: %v", rs)
	}

	// 入站
	inSettings, err := p.BuildInboundSettings()
	if err != nil {
		t.Fatalf("inbound settings err: %v", err)
	}
	var m map[string]any
	json.Unmarshal([]byte(inSettings), &m)
	clients := m["clients"].([]any)
	c0 := clients[0].(map[string]any)
	if c0["id"] != "6e5a0a5b-1b3f-4f3a-9c0e-8f1d2e3a4b5c" {
		t.Fatalf("client id wrong: %v", c0)
	}
}

func TestParseVmessBase64(t *testing.T) {
	// vmess 老格式 base64(json)
	payload := `{"v":"2","ps":"美国节点","add":"8.8.8.8","port":"8443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","net":"ws","type":"none","host":"example.com","path":"/ws","tls":"tls","sni":"example.com"}`
	enc := base64Std(payload)
	link := "vmess://" + enc
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.Protocol != "vmess" || p.Address != "8.8.8.8" || p.Port != 8443 {
		t.Fatalf("basic wrong: %+v", p)
	}
	if p.Remark != "美国节点" || p.Network != "ws" || p.Path != "/ws" || p.Host != "example.com" || p.Security != "tls" {
		t.Fatalf("fields wrong: %+v", p)
	}
	ob := p.BuildOutbound("vmess-tag")
	ss := ob["streamSettings"].(map[string]any)
	ws := ss["wsSettings"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Fatalf("ws path wrong: %v", ws)
	}
}

func TestParseTrojan(t *testing.T) {
	link := "trojan://my-password@5.6.7.8:443?security=tls&sni=cdn.example.com&type=ws&path=%2Ftrojan&host=cdn.example.com#日本节点"
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.Password != "my-password" || p.Address != "5.6.7.8" || p.Port != 443 {
		t.Fatalf("basic wrong: %+v", p)
	}
	if p.Network != "ws" || p.SNI != "cdn.example.com" {
		t.Fatalf("fields wrong: %+v", p)
	}
	ob := p.BuildOutbound("trojan-tag")
	settings := ob["settings"].(map[string]any)
	servers := settings["servers"].([]any)
	s0 := servers[0].(map[string]any)
	if s0["password"] != "my-password" || s0["address"] != "5.6.7.8" {
		t.Fatalf("server wrong: %v", s0)
	}
}

func TestParseSS(t *testing.T) {
	// ss://base64(method:password)@host:port
	enc := base64Std("aes-256-gcm:secret123")
	link := "ss://" + enc + "@9.9.9.9:8388#新加坡节点"
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.Protocol != "shadowsocks" || p.Method != "aes-256-gcm" || p.Password != "secret123" {
		t.Fatalf("ss wrong: %+v", p)
	}
	if p.Address != "9.9.9.9" || p.Port != 8388 {
		t.Fatalf("addr wrong: %+v", p)
	}
	ob := p.BuildOutbound("ss-tag")
	settings := ob["settings"].(map[string]any)
	servers := settings["servers"].([]any)
	s0 := servers[0].(map[string]any)
	if s0["method"] != "aes-256-gcm" || s0["password"] != "secret123" {
		t.Fatalf("ss server wrong: %v", s0)
	}
}

func TestParseSSPlain(t *testing.T) {
	// ss://method:password@host:port 明文格式
	link := "ss://chacha20-ietf-poly1305:pass@1.1.1.1:9000#测试"
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.Method != "chacha20-ietf-poly1305" || p.Password != "pass" {
		t.Fatalf("ss plain wrong: %+v", p)
	}
}

func TestParseVmessNewFormat(t *testing.T) {
	// vmess 新格式 uuid@host:port
	link := "vmess://bbbbbbbb-1111-2222-3333-444444444444@2.2.2.2:10086?type=tcp&security=none#韩国节点"
	p, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if p.UUID != "bbbbbbbb-1111-2222-3333-444444444444" || p.Address != "2.2.2.2" || p.Port != 10086 {
		t.Fatalf("vmess new wrong: %+v", p)
	}
	if p.Security != "none" || p.Network != "tcp" {
		t.Fatalf("security wrong: %+v", p)
	}
}

func TestParseUnsupported(t *testing.T) {
	_, err := ParseShareLink("unknownproto://abc@1.1.1.1:443")
	if err == nil {
		t.Fatalf("should error on unsupported protocol")
	}
}

func base64Std(s string) string {
	return stdB64(s)
}

func stdB64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
