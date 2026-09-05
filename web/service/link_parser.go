package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParsedLink 解析后的分享链接（协议无关的中间表示）
type ParsedLink struct {
	Protocol      string   // vless / vmess / trojan / shadowsocks / socks / hysteria2 / anytls / tuic / naive
	Remark        string   // # 后的备注
	Address       string   // 服务器地址
	Port          int      // 服务器端口
	UUID          string   // vless/vmess/tuic 的 id
	AlterID       int      // vmess alterId
	Password      string   // trojan 密码 / ss 密码 / anytls 密码 / tuic 密码 / naive 密码 / hysteria auth
	Username      string   // socks 用户名 / naive 用户名
	Method        string   // ss 加密方法
	Flow          string   // vless flow (xtls-rprx-vision)
	Security      string   // none / tls / reality
	Fingerprint   string   // reality/tls fp
	SNI           string   // reality/tls serverName
	PublicKey     string   // reality pbk
	ShortID       string   // reality sid
	SpiderX       string   // reality spx
	Network       string   // tcp / ws / grpc / httpupgrade / xhttp / hysteria
	Path          string   // ws/grpc/httpupgrade/xhttp path
	Host          string   // ws host / grpc authority / xhttp host
	ServiceName   string   // grpc serviceName
	ALPN          []string // tls/reality alpn
	AllowInsecure bool     // tls allowInsecure
	// 扩展协议参数
	Auth              string // hysteria auth（老格式 ?auth=xxx）
	UpMbps            int    // hysteria upMbps
	DownMbps          int    // hysteria downMbps
	Obfs              string // hysteria obfs (salamander) / 老格式 obfs (xplus)
	ObfsPassword      string // hysteria obfs-password
	CongestionControl string // tuic congestion_control
	UdpRelayMode      string // tuic udp_relay_mode
	ZeroRTT           bool   // tuic zero_rtt_handshake
	HeartbeatInterval int    // tuic heartbeat_interval
}

// ParseShareLink 解析 vless/vmess/trojan/ss/socks/hysteria2/anytls/tuic/naive 分享链接
func ParseShareLink(link string) (*ParsedLink, error) {
	link = strings.TrimSpace(link)
	lower := strings.ToLower(link)
	var p *ParsedLink
	var err error
	switch {
	case strings.HasPrefix(lower, "vless://"):
		p, err = parseVless(link)
	case strings.HasPrefix(lower, "vmess://"):
		p, err = parseVmess(link)
	case strings.HasPrefix(lower, "trojan://"):
		p, err = parseTrojan(link)
	case strings.HasPrefix(lower, "ss://"):
		p, err = parseSS(link)
	case strings.HasPrefix(lower, "socks5://"):
		p, err = parseSocks(link, "socks")
	case strings.HasPrefix(lower, "socks://"):
		p, err = parseSocks(link, "socks")
	case strings.HasPrefix(lower, "hysteria2://"):
		p, err = parseHysteria(link, "hysteria2")
	case strings.HasPrefix(lower, "hysteria://"):
		p, err = parseHysteria(link, "hysteria")
	case strings.HasPrefix(lower, "anytls://"):
		p, err = parseAnyTLS(link)
	case strings.HasPrefix(lower, "tuic://"):
		p, err = parseTuic(link)
	case strings.HasPrefix(lower, "naive+https://"):
		p, err = parseNaive(link)
	case strings.HasPrefix(lower, "naive+http://"):
		p, err = parseNaive(link)
	case strings.HasPrefix(lower, "naive://"):
		p, err = parseNaive(link)
	default:
		return nil, fmt.Errorf("不支持的协议（仅支持 vless/vmess/trojan/ss/socks/hysteria2/anytls/tuic/naive）")
	}
	if err != nil {
		return nil, err
	}
	if p.Remark == "" {
		p.Remark = p.Address
	}
	return p, nil
}

// parseHysteria 解析 hysteria2://auth@host:port?security=tls&sni=...&alpn=...&obfs=...&obfs-password=...#remark
// 兼容老格式 hysteria://host:port?auth=xxx&upmbps=...&downmbps=...&obfs=...
func parseHysteria(link string, proto string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(strings.TrimPrefix(link, "hysteria2://"), "hysteria://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol:      proto,
		Remark:        unescape(remark),
		Security:      q.Get("security"),
		Fingerprint:   q.Get("fp"),
		SNI:           q.Get("sni"),
		AllowInsecure: q.Get("insecure") == "1" || q.Get("allowInsecure") == "1",
		Network:       "hysteria",
	}
	if p.Security == "" {
		p.Security = "tls"
	}
	// 解析 auth（新格式 auth@host:port；老格式 query ?auth=xxx）
	auth := q.Get("auth")
	hostport := userhost
	at := strings.LastIndex(userhost, "@")
	if at >= 0 {
		auth = userhost[:at]
		hostport = userhost[at+1:]
	}
	if a := q.Get("password"); a != "" && auth == "" {
		auth = a
	}
	p.Auth = unescape(auth)
	p.Password = unescape(auth)
	if v, e := strconv.Atoi(q.Get("upmbps")); e == nil {
		p.UpMbps = v
	}
	if v, e := strconv.Atoi(q.Get("downmbps")); e == nil {
		p.DownMbps = v
	}
	p.Obfs = q.Get("obfs")
	p.ObfsPassword = q.Get("obfs-password")
	if p.ObfsPassword == "" {
		p.ObfsPassword = q.Get("obfsParam")
	}
	if alpn := q.Get("alpn"); alpn != "" {
		for _, a := range strings.Split(alpn, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				p.ALPN = append(p.ALPN, a)
			}
		}
	}
	host, port, err := parseHostPort(hostport)
	if err != nil {
		return nil, err
	}
	p.Address = host
	p.Port = port
	return p, nil
}

// parseAnyTLS 解析 anytls://password@host:port?security=tls|reality&sni=...&pbk=...&sid=...&fp=...#remark
func parseAnyTLS(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "anytls://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("anytls 链接缺少 @")
	}
	password := userhost[:at]
	host, port, err := parseHostPort(userhost[at+1:])
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "anytls",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		Password: unescape(password),
		Security: q.Get("security"),
		SNI:      q.Get("sni"),
	}
	if p.Security == "" {
		p.Security = "tls" // anytls 默认 tls
	}
	applyStreamParams(p, q)
	return p, nil
}

// parseTuic 解析 tuic://uuid:password@host:port?congestion_control=bbr&udp_relay_mode=native&security=tls&sni=...#remark
func parseTuic(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "tuic://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("tuic 链接缺少 @")
	}
	userinfo := userhost[:at]
	hostport := userhost[at+1:]
	uuid, password := userinfo, ""
	if idx := strings.Index(userinfo, ":"); idx >= 0 {
		uuid = userinfo[:idx]
		password = userinfo[idx+1:]
	}
	host, port, err := parseHostPort(hostport)
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol:          "tuic",
		Remark:            unescape(remark),
		Address:           host,
		Port:              port,
		UUID:              unescape(uuid),
		Password:          unescape(password),
		Security:          q.Get("security"),
		Fingerprint:       q.Get("fp"),
		SNI:               q.Get("sni"),
		CongestionControl: q.Get("congestion_control"),
		UdpRelayMode:      q.Get("udp_relay_mode"),
		AllowInsecure:     q.Get("insecure") == "1" || q.Get("allowInsecure") == "1",
	}
	if p.Security == "" {
		p.Security = "tls"
	}
	if p.CongestionControl == "" {
		p.CongestionControl = "bbr"
	}
	if p.UdpRelayMode == "" {
		p.UdpRelayMode = "native"
	}
	p.ZeroRTT = q.Get("zero_rtt_handshake") == "1" || q.Get("zero_rtt_handshake") == "true"
	if alpn := q.Get("alpn"); alpn != "" {
		for _, a := range strings.Split(alpn, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				p.ALPN = append(p.ALPN, a)
			}
		}
	}
	return p, nil
}

// parseNaive 解析 naive+https://user:pass@host:port#remark / naive://user:pass@host:port?encryption=none#remark
func parseNaive(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(link, "naive+https://"), "naive+http://"), "naive://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("naive 链接缺少 @")
	}
	userinfo := userhost[:at]
	hostport := userhost[at+1:]
	username, password := userinfo, ""
	if idx := strings.Index(userinfo, ":"); idx >= 0 {
		username = userinfo[:idx]
		password = userinfo[idx+1:]
	}
	host, port, err := parseHostPort(hostport)
	if err != nil {
		return nil, err
	}
	_, err = url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "naive",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		Username: unescape(username),
		Password: unescape(password),
		Security: "tls",
		Network:  "tcp",
	}
	return p, nil
}

// parseSocks 解析 socks://[user:pass@]host:port 代理解析格式
// 支持 socks://user:pass@host:port / socks5://host:port / socks://host:port?type=socks5
func parseSocks(link string, proto string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(strings.TrimPrefix(link, "socks5://"), "socks://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	username, password := "", ""
	hostport := userhost
	if at >= 0 {
		userinfo := userhost[:at]
		hostport = userhost[at+1:]
		if idx := strings.Index(userinfo, ":"); idx >= 0 {
			username = unescape(userinfo[:idx])
			password = unescape(userinfo[idx+1:])
		} else {
			username = unescape(userinfo)
		}
	}
	host, port, err := parseHostPort(hostport)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "socks",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		Username: username,
		Password: password,
		Security: "none",
	}
	// 兼容 ?type=socks5 / ?tls 等附加参数（忽略未知参数不报错）
	if q, err := url.ParseQuery(queryStr); err == nil {
		_ = q
	}
	return p, nil
}

// urlDecodeHost 解析 userinfo@host:port，host 可能带 []
func parseHostPort(hostport string) (host string, port int, err error) {
	if strings.HasPrefix(hostport, "[") {
		// IPv6
		end := strings.Index(hostport, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("IPv6 地址格式错误")
		}
		host = hostport[1:end]
		rest := hostport[end+1:]
		if strings.HasPrefix(rest, ":") {
			port, err = strconv.Atoi(rest[1:])
			if err != nil {
				return "", 0, fmt.Errorf("端口格式错误")
			}
		}
		return host, port, nil
	}
	idx := strings.LastIndex(hostport, ":")
	if idx < 0 {
		// 无端口
		if p, e := strconv.Atoi(hostport); e == nil {
			return "", p, nil // 纯端口? 视为异常
		}
		return hostport, 0, nil
	}
	host = hostport[:idx]
	portStr := hostport[idx+1:]
	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("端口格式错误: %s", portStr)
	}
	return host, port, nil
}

// unescape 安全解码 URL 编码文本
func unescape(s string) string {
	if s == "" {
		return s
	}
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// base64Decode 兼容标准/URL 安全的 base64 解码（去掉 padding）
func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func parseVless(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "vless://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	// 找第一个 ? 分离 query
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("vless 链接缺少 @")
	}
	uuid := userhost[:at]
	host, port, err := parseHostPort(userhost[at+1:])
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "vless",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		UUID:     uuid,
		Flow:     q.Get("flow"),
		Security: q.Get("security"),
	}
	if p.Security == "" {
		p.Security = "none"
	}
	applyStreamParams(p, q)
	return p, nil
}

func parseTrojan(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "trojan://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	userhost := rest
	queryStr := ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		userhost = rest[:idx]
		queryStr = rest[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("trojan 链接缺少 @")
	}
	password := userhost[:at]
	host, port, err := parseHostPort(userhost[at+1:])
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "trojan",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		Password: password,
		Security: q.Get("security"),
	}
	if p.Security == "" {
		p.Security = "tls" // trojan 默认 tls
	}
	applyStreamParams(p, q)
	return p, nil
}

func parseSS(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "ss://")
	remark := ""
	if idx := strings.LastIndex(rest, "#"); idx >= 0 {
		remark = rest[idx+1:]
		rest = rest[:idx]
	}
	// ss 链接可能是 base64(method:password)@host:port 或 method:password@host:port
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		// 整串 base64
		b, err := base64Decode(rest)
		if err != nil {
			return nil, fmt.Errorf("ss 链接格式无法识别")
		}
		return parseSS(string(b))
	}
	userinfo := rest[:at]
	hostport := rest[at+1:]
	host, port, err := parseHostPort(hostport)
	if err != nil {
		return nil, err
	}
	var method, password string
	// 尝试 base64 解 userinfo
	if b, err := base64Decode(userinfo); err == nil {
		s := string(b)
		if idx := strings.Index(s, ":"); idx > 0 {
			method = s[:idx]
			password = s[idx+1:]
		}
	}
	if method == "" {
		// 明文 method:password 或 method:base64password
		idx := strings.Index(userinfo, ":")
		if idx <= 0 {
			return nil, fmt.Errorf("ss 链接缺少 method:password")
		}
		method = userinfo[:idx]
		pwRaw := userinfo[idx+1:]
		// ss2022 密码是 base64
		if b, err := base64Decode(pwRaw); err == nil && looksLikePrintable(b) {
			password = string(b)
		} else {
			password = pwRaw
		}
	}
	return &ParsedLink{
		Protocol: "shadowsocks",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		Method:   method,
		Password: password,
	}, nil
}

func looksLikePrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func parseVmess(link string) (*ParsedLink, error) {
	rest := strings.TrimPrefix(link, "vmess://")
	// 老格式：vmess://base64(json)
	if b, err := base64Decode(rest); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			port, _ := strconv.Atoi(fmt.Sprintf("%v", m["port"]))
			alterID, _ := strconv.Atoi(fmt.Sprintf("%v", m["aid"]))
			p := &ParsedLink{
				Protocol:    "vmess",
				Remark:      unescape(fmt.Sprintf("%v", m["ps"])),
				Address:     fmt.Sprintf("%v", m["add"]),
				Port:        port,
				UUID:        fmt.Sprintf("%v", m["id"]),
				AlterID:     alterID,
				Network:     fmt.Sprintf("%v", m["net"]),
				Path:        fmt.Sprintf("%v", m["path"]),
				Host:        fmt.Sprintf("%v", m["host"]),
				Security:    fmt.Sprintf("%v", m["tls"]),
				Fingerprint: fmt.Sprintf("%v", m["fp"]),
				SNI:         fmt.Sprintf("%v", m["sni"]),
				Flow:        fmt.Sprintf("%v", m["flow"]),
			}
			if p.Network == "" {
				p.Network = "tcp"
			}
			if p.Security == "" || p.Security == "<nil>" || p.Security == "0" {
				p.Security = "none"
			}
			if p.Fingerprint == "<nil>" {
				p.Fingerprint = ""
			}
			if p.SNI == "<nil>" {
				p.SNI = ""
			}
			if p.Flow == "<nil>" {
				p.Flow = ""
			}
			if p.Address == "<nil>" || p.Address == "" {
				return nil, fmt.Errorf("vmess 链接缺少服务器地址")
			}
			return p, nil
		}
	}
	// 新格式：vmess://uuid@host:port?params
	rest2 := rest
	remark := ""
	if idx := strings.LastIndex(rest2, "#"); idx >= 0 {
		remark = rest2[idx+1:]
		rest2 = rest2[:idx]
	}
	userhost := rest2
	queryStr := ""
	if idx := strings.Index(rest2, "?"); idx >= 0 {
		userhost = rest2[:idx]
		queryStr = rest2[idx+1:]
	}
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil, fmt.Errorf("vmess 链接格式无法识别")
	}
	uuid := userhost[:at]
	host, port, err := parseHostPort(userhost[at+1:])
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, err
	}
	p := &ParsedLink{
		Protocol: "vmess",
		Remark:   unescape(remark),
		Address:  host,
		Port:     port,
		UUID:     uuid,
		Security: q.Get("security"),
	}
	if p.Security == "" {
		p.Security = "none"
	}
	applyStreamParams(p, q)
	return p, nil
}

// applyStreamParams 把 query 参数填充到传输/安全相关字段
func applyStreamParams(p *ParsedLink, q url.Values) {
	p.Network = q.Get("type")
	if p.Network == "" {
		p.Network = "tcp"
	}
	p.Fingerprint = q.Get("fp")
	p.SNI = q.Get("sni")
	p.PublicKey = q.Get("pbk")
	p.ShortID = q.Get("sid")
	p.SpiderX = q.Get("spx")
	p.Path = q.Get("path")
	p.Host = q.Get("host")
	p.ServiceName = q.Get("serviceName")
	if alpn := q.Get("alpn"); alpn != "" {
		for _, a := range strings.Split(alpn, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				p.ALPN = append(p.ALPN, a)
			}
		}
	}
	if v := q.Get("allowInsecure"); v == "1" || v == "true" {
		p.AllowInsecure = true
	}
}

// BuildStreamSettings 生成出站/入站通用 streamSettings
func (p *ParsedLink) BuildStreamSettings() map[string]any {
	// network 为空（socks/无传输层出站）时返回空，避免 xray 解析 network:"" 崩溃
	if p.Network == "" {
		return map[string]any{}
	}
	ss := map[string]any{
		"network":  p.Network,
		"security": p.Security,
	}
	switch p.Network {
	case "ws":
		ws := map[string]any{"path": p.Path}
		if p.Host != "" {
			ws["headers"] = map[string]any{"Host": p.Host}
		}
		ss["wsSettings"] = ws
	case "grpc":
		grpc := map[string]any{}
		if p.ServiceName != "" {
			grpc["serviceName"] = p.ServiceName
		}
		if p.Host != "" {
			grpc["authority"] = p.Host
		}
		ss["grpcSettings"] = grpc
	case "httpupgrade":
		ss["httpupgradeSettings"] = map[string]any{
			"path": p.Path,
			"host": p.Host,
		}
	case "xhttp":
		ss["xhttpSettings"] = map[string]any{
			"path": p.Path,
			"host": p.Host,
		}
	case "hysteria":
		// xray hysteria 出站 streamSettings：hysteriaSettings 承载 version/auth，network 固定 hysteria
		hy := map[string]any{"version": 2}
		if p.Auth != "" {
			hy["auth"] = p.Auth
		} else if p.Password != "" {
			hy["auth"] = p.Password
		}
		ss["hysteriaSettings"] = hy
	}
	switch p.Security {
	case "tls":
		// SNI 缺省回退到 address：IP 直连或无 sni 参数的链接若留空，
		// xray 会拿协议名当 SNI 校验证书导致 CRYPTO_ERROR（hy2 实锤）
		sni := p.SNI
		if sni == "" {
			sni = p.Address
		}
		tlsS := map[string]any{
			"serverName":    sni,
			"allowInsecure": p.AllowInsecure,
		}
		if p.Fingerprint != "" {
			tlsS["fingerprint"] = p.Fingerprint
		}
		if len(p.ALPN) > 0 {
			tlsS["alpn"] = p.ALPN
		}
		ss["tlsSettings"] = tlsS
	case "reality":
		r := map[string]any{
			"serverName":  p.SNI,
			"fingerprint": p.Fingerprint,
		}
		if p.PublicKey != "" {
			r["publicKey"] = p.PublicKey
		}
		if p.ShortID != "" {
			r["shortId"] = p.ShortID
		}
		if p.SpiderX != "" {
			r["spiderX"] = p.SpiderX
		}
		if len(p.ALPN) > 0 {
			r["alpn"] = p.ALPN
		}
		ss["realitySettings"] = r
	}
	return ss
}

// BuildOutbound 生成 xray 出站 JSON（用于一键导入出站）
func (p *ParsedLink) BuildOutbound(tag string) map[string]any {
	// xray 的 outbound protocol 名与分享链接协议名的映射：
	// hysteria2 链接 → xray outbound protocol 用 "hysteria"（xray 26.x config id 只认 hysteria）
	proto := p.Protocol
	if proto == "hysteria2" {
		proto = "hysteria"
	}
	ob := map[string]any{
		"tag":      tag,
		"protocol": proto,
		"settings": p.buildOutboundSettings(),
	}
	ss := p.BuildStreamSettings()
	if len(ss) > 0 {
		ob["streamSettings"] = ss
	}
	return ob
}

// BuildSingBoxOutbound 生成 sing-box 风格出站 JSON（anytls/tuic/naive 由 sing-box 承载）
func (p *ParsedLink) BuildSingBoxOutbound(tag string) map[string]any {
	ob := map[string]any{
		"type":        p.Protocol,
		"tag":         tag,
		"server":      p.Address,
		"server_port": p.Port,
	}
	switch p.Protocol {
	case "anytls":
		ob["password"] = p.Password
		if p.Security != "" && p.Security != "none" {
			tls := map[string]any{"enabled": true}
			if p.SNI != "" {
				tls["server_name"] = p.SNI
			}
			if p.Fingerprint != "" {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": p.Fingerprint}
			}
			ob["tls"] = tls
		}
	case "tuic":
		ob["uuid"] = p.UUID
		ob["password"] = p.Password
		// sing-box tuic 出站 udp_relay_mode 只支持 native/udp_over_quic，默认 native；
		// 旧版解析可能把分享链接的 quic 误存，统一落到 native 以保证连通
		mode := p.UdpRelayMode
		if mode == "" || mode == "quic" {
			mode = "native"
		}
		ob["udp_relay_mode"] = mode
		if p.CongestionControl != "" {
			ob["congestion_control"] = p.CongestionControl
		}
		if p.Security != "" && p.Security != "none" {
			tls := map[string]any{"enabled": true}
			if p.SNI != "" {
				tls["server_name"] = p.SNI
			}
			// tuic 需要 ALPN 协商 h3/h2，缺省注入 h3 否则服务端报 no application protocol
			if len(p.ALPN) > 0 {
				tls["alpn"] = p.ALPN
			} else {
				tls["alpn"] = []string{"h3"}
			}
			ob["tls"] = tls
		}
	case "naive":
		ob["username"] = p.Username
		ob["password"] = p.Password
		if p.Security != "" && p.Security != "none" {
			tls := map[string]any{"enabled": true}
			// naive 走 TLS 时必须校验证书，server_name 缺省用服务器地址兜底
			sn := p.SNI
			if sn == "" {
				sn = p.Address
			}
			if sn != "" {
				tls["server_name"] = sn
			}
			if p.AllowInsecure {
				tls["insecure"] = true
			}
			ob["tls"] = tls
		}
	}
	return ob
}

func (p *ParsedLink) buildOutboundSettings() map[string]any {
	switch p.Protocol {
	case "vless":
		user := map[string]any{
			"id":         p.UUID,
			"encryption": "none",
		}
		if p.Flow != "" {
			user["flow"] = p.Flow
		}
		return map[string]any{
			"vnext": []any{map[string]any{
				"address": p.Address,
				"port":    p.Port,
				"users":   []any{user},
			}},
		}
	case "vmess":
		return map[string]any{
			"vnext": []any{map[string]any{
				"address": p.Address,
				"port":    p.Port,
				"users": []any{map[string]any{
					"id":       p.UUID,
					"alterId":  p.AlterID,
					"security": "auto",
				}},
			}},
		}
	case "trojan":
		server := map[string]any{
			"address":  p.Address,
			"port":     p.Port,
			"password": p.Password,
		}
		if p.Flow != "" {
			server["flow"] = p.Flow
		}
		return map[string]any{
			"servers": []any{server},
		}
	case "shadowsocks":
		return map[string]any{
			"servers": []any{map[string]any{
				"address":  p.Address,
				"port":     p.Port,
				"method":   p.Method,
				"password": p.Password,
			}},
		}
	case "socks":
		// xray socks 出站：settings.servers 数组，可选 users[{user,pass}]
		server := map[string]any{
			"address": p.Address,
			"port":    p.Port,
		}
		if p.Username != "" || p.Password != "" {
			server["users"] = []any{map[string]any{
				"user": p.Username,
				"pass": p.Password,
			}}
		}
		return map[string]any{
			"servers": []any{server},
		}
	case "hysteria", "hysteria2":
		// xray hysteria2 出站：settings.version/address/port（xray hysteria outbound 原生结构）
		return map[string]any{
			"version": 2,
			"address": p.Address,
			"port":    p.Port,
		}
	case "anytls":
		// sing-box anytls 出站
		return map[string]any{
			"password": p.Password,
		}
	case "tuic":
		// sing-box tuic 出站
		return map[string]any{
			"uuid":     p.UUID,
			"password": p.Password,
		}
	case "naive":
		// sing-box naive 出站
		return map[string]any{
			"username": p.Username,
			"password": p.Password,
		}
	}
	return map[string]any{}
}

// BuildInboundSettings 生成入站 settings JSON 字符串（用于一键导入入站）
func (p *ParsedLink) BuildInboundSettings() (string, error) {
	var settings map[string]any
	switch p.Protocol {
	case "vless":
		client := map[string]any{
			"id":      p.UUID,
			"flow":    p.Flow,
			"enable":  true,
			"email":   "",
			"limitIp": 0,
			"totalGB": 0,
		}
		settings = map[string]any{
			"clients":    []any{client},
			"decryption": "none",
			"fallbacks":  []any{},
		}
	case "vmess":
		settings = map[string]any{
			"clients": []any{map[string]any{
				"id":      p.UUID,
				"alterId": p.AlterID,
				"email":   "",
				"limitIp": 0,
				"totalGB": 0,
				"enable":  true,
			}},
			"disableInsecureEncryption": false,
		}
	case "trojan":
		settings = map[string]any{
			"clients": []any{map[string]any{
				"password": p.Password,
				"email":    "",
				"limitIp":  0,
				"totalGB":  0,
				"enable":   true,
			}},
			"fallbacks": []any{},
		}
	case "shadowsocks":
		settings = map[string]any{
			"method":   p.Method,
			"password": p.Password,
			"network":  "tcp",
			"udp":      true,
		}
	default:
		return "", fmt.Errorf("不支持的协议")
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildInboundStreamSettings 生成入站 streamSettings JSON 字符串
func (p *ParsedLink) BuildInboundStreamSettings() (string, error) {
	ss := p.BuildStreamSettings()
	// 入站 reality 需要 privateKey 而不是 publicKey，无法从分享链接还原；置空以避免配置错误
	if p.Security == "reality" {
		if r, ok := ss["realitySettings"].(map[string]any); ok {
			delete(r, "publicKey")
			delete(r, "spiderX")
			delete(r, "shortId")
		}
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
