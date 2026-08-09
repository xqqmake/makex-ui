package model

import (
	"encoding/json"
	"fmt"

	"x-ui/util/json_util"
	"x-ui/xray"
)

type Protocol string

const (
	VMESS       Protocol = "vmess"
	VLESS       Protocol = "vless"
	Tunnel      Protocol = "tunnel"
	HTTP        Protocol = "http"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"
	Socks       Protocol = "socks"
	WireGuard   Protocol = "wireguard"

	// UI stores Hysteria v1 and v2 both as "hysteria" and uses
	// settings.version to discriminate. Imports from outside the panel
	// can carry the literal "hysteria2" string, so IsHysteria below
	// accepts both.
	Hysteria  Protocol = "hysteria"
	Hysteria2 Protocol = "hysteria2"
)

// IsHysteria returns true for both "hysteria" and "hysteria2".
// Use instead of a bare ==model.Hysteria check: a v2 inbound stored
// with the literal v2 string would otherwise fall through.
func IsHysteria(p Protocol) bool {
	return p == Hysteria || p == Hysteria2
}

type User struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Inbound struct {
	Id          int                  `json:"id" form:"id" gorm:"primaryKey"`
	UserId      int                  `json:"-"`
	Up          int64                `json:"up" form:"up"`
	Down        int64                `json:"down" form:"down"`
	Total       int64                `json:"total" form:"total"`
	AllTime     int64                `json:"allTime" form:"allTime" gorm:"default:0"`
	Remark      string               `json:"remark" form:"remark"`
	Enable      bool                 `json:"enable" form:"enable"`
	ExpiryTime  int64                `json:"expiryTime" form:"expiryTime"`

	// 中文注释: 新增设备限制字段，用于存储每个入站的设备数限制。
	// gorm:"column:device_limit;default:0" 定义了数据库中的字段名和默认值。
	DeviceLimit   int                  `json:"deviceLimit" form:"deviceLimit" gorm:"column:device_limit;default:0"`

	ClientStats []xray.ClientTraffic `gorm:"foreignKey:InboundId;references:Id" json:"clientStats" form:"clientStats"`

	// config part
	Listen         string   `json:"listen" form:"listen"`
	Port           int      `json:"port" form:"port"`
	Protocol       Protocol `json:"protocol" form:"protocol"`
	Settings       string   `json:"settings" form:"settings"`
	StreamSettings string   `json:"streamSettings" form:"streamSettings"`
	Tag            string   `json:"tag" form:"tag" gorm:"unique"`
	Sniffing       string   `json:"sniffing" form:"sniffing"`
}

type OutboundTraffics struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Tag   string `json:"tag" form:"tag" gorm:"unique"`
	Up    int64  `json:"up" form:"up" gorm:"default:0"`
	Down  int64  `json:"down" form:"down" gorm:"default:0"`
	Total int64  `json:"total" form:"total" gorm:"default:0"`
}

type InboundClientIps struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientEmail string `json:"clientEmail" form:"clientEmail" gorm:"unique"`
	Ips         string `json:"ips" form:"ips"`
}

type HistoryOfSeeders struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SeederName string `json:"seederName"`
}

func (i *Inbound) GenXrayInboundConfig() *xray.InboundConfig {
	listen := i.Listen
	if listen != "" {
		listen = fmt.Sprintf("\"%v\"", listen)
	}
	port := json_util.RawMessage(fmt.Sprintf("%d", i.Port))
	// Hysteria port hopping: 服务端只监听主端口，跳跃范围由 nft/iptables DNAT 规则转发到主端口。
	// 若把范围直接拼进 xray PortList（如 "48998,30000-45000"），xray 会为范围内每个端口各开
	// 一个 UDP socket（app/proxyman/inbound/always.go 展开 PortList），大范围（如 15000 端口）
	// 会耗尽内存导致 OOM 崩溃。跳跃范围仍保留在 streamSettings.hysteria.portHopping 供 UI 展示
	// 与订阅链接生成（mport），并驱动 nft DNAT 规则（见 xray/nft.go）。
	if IsHysteria(i.Protocol) {
		_ = PortHoppingPorts(i.StreamSettings)
	}
	return &xray.InboundConfig{
		Listen:         json_util.RawMessage(listen),
		Port:           port,
		Protocol:       string(i.Protocol),
		Settings:       json_util.RawMessage(i.Settings),
		StreamSettings: json_util.RawMessage(i.StreamSettings),
		Tag:            i.Tag,
		Sniffing:       json_util.RawMessage(i.Sniffing),
	}
}

// PortHoppingPorts extracts the Hysteria port-hopping range list from the
// inbound streamSettings JSON (key: streamSettings.hysteria.portHopping).
// Returns "" when disabled or missing.
func PortHoppingPorts(streamSettings string) string {
	var stream map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return ""
	}
	hysteria, ok := stream["hysteria"].(map[string]any)
	if !ok {
		return ""
	}
	portHopping, ok := hysteria["portHopping"].(map[string]any)
	if !ok {
		return ""
	}
	enabled, _ := portHopping["enabled"].(bool)
	if !enabled {
		return ""
	}
	ports, _ := portHopping["ports"].(string)
	return ports
}

type Setting struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Key   string `json:"key" form:"key"`
	Value string `json:"value" form:"value"`
}

type Client struct {
	ID         string `json:"id"`
	Security   string `json:"security"`
	Password   string `json:"password"`
	Auth       string `json:"auth,omitempty"` // Hysteria/Hysteria2 入站认证密码

	// 中文注释: 新增“限速”字段，单位 KB/s，0 表示不限速。
    SpeedLimit   int           `json:"speedLimit" form:"speedLimit"`
	
	Flow       string `json:"flow"`
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB" form:"totalGB"`
	ExpiryTime int64  `json:"expiryTime" form:"expiryTime"`
	Enable     bool   `json:"enable" form:"enable"`
	TgID       int64  `json:"tgId" form:"tgId"`
	SubID      string `json:"subId" form:"subId"`
	Comment    string `json:"comment" form:"comment"`
	Reset      int    `json:"reset" form:"reset"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	UpdatedAt  int64  `json:"updated_at,omitempty"`
}

type VLESSSettings struct {
	Clients    []Client `json:"clients"`
	Decryption string   `json:"decryption"`
	Encryption string   `json:"encryption"`
	Fallbacks  []any    `json:"fallbacks"`
}
