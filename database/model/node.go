package model

import (
	"time"
)

// Node 节点信息
type Node struct {
	Id               int       `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	UUID             string    `json:"uuid" form:"uuid" gorm:"type:varchar(36);uniqueIndex"`
	Token            string    `json:"token" form:"token" gorm:"type:varchar(255);uniqueIndex"`
	Name             string    `json:"name" form:"name" gorm:"type:varchar(100)"`
	Remark           string    `json:"remark" form:"remark" gorm:"type:text"`
	Host             string    `json:"host" form:"host" gorm:"type:varchar(100)"`
	Port             int       `json:"port" form:"port" gorm:"default:22"`
	Protocol         string    `json:"protocol" form:"protocol" gorm:"type:varchar(20);default:'ssh'"` // ssh, api
	Status           string    `json:"status" form:"status" gorm:"type:varchar(20);default:'offline'"` // online, offline, error
	LastOnline       time.Time `json:"lastOnline"`
	LastReport       time.Time `json:"lastReport"`
	
	// 系统信息（首次上报时自动填充）
	OS               string    `json:"os" form:"os" gorm:"type:varchar(100)"`
	Arch             string    `json:"arch" form:"arch" gorm:"type:varchar(50)"`
	KernelVersion    string    `json:"kernelVersion" form:"kernelVersion" gorm:"type:varchar(100)"`
	CPUName          string    `json:"cpuName" form:"cpuName" gorm:"type:varchar(100)"`
	CPUCores         int       `json:"cpuCores" form:"cpuCores"`
	MemTotal         int64     `json:"memTotal" form:"memTotal"`
	DiskTotal        int64     `json:"diskTotal" form:"diskTotal"`
	
	// 网络信息
	IPv4             string    `json:"ipv4" form:"ipv4" gorm:"type:varchar(100)"`
	IPv6             string    `json:"ipv6" form:"ipv6" gorm:"type:varchar(100)"`
	Region           string    `json:"region" form:"region" gorm:"type:varchar(100)"`
	
	// 配置
	Group            string    `json:"group" form:"group" gorm:"type:varchar(100)"`
	Tags             string    `json:"tags" form:"tags" gorm:"type:text"`
	Hidden           bool      `json:"hidden" form:"hidden" gorm:"default:false"`
	
	// 安装选项（JSON格式存储）
	InstallOptions   string    `json:"installOptions" form:"installOptions" gorm:"type:text"`

	// SSH凭据（WebSSH终端使用，密码加密存储，API不返回明文）
	SSHUser          string    `json:"sshUser" form:"sshUser" gorm:"type:varchar(100)"`
	SSHPassword      string    `json:"sshPassword" form:"sshPassword" gorm:"type:text"`
}

// NodeRecord 节点监控记录
type NodeRecord struct {
	Id           int       `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId       int       `json:"nodeId" gorm:"index"`
	UUID         string    `json:"uuid" gorm:"index"`
	Time         time.Time `json:"time" gorm:"index"`
	
	// CPU
	CPUUsage     float64   `json:"cpuUsage" gorm:"type:decimal(5,2)"`
	
	// 内存
	MemUsed      int64     `json:"memUsed"`
	MemTotal     int64     `json:"memTotal"`
	SwapUsed     int64     `json:"swapUsed"`
	SwapTotal    int64     `json:"swapTotal"`
	
	// 磁盘
	DiskUsed     int64     `json:"diskUsed"`
	DiskTotal    int64     `json:"diskTotal"`
	
	// 网络
	NetIn        int64     `json:"netIn"`
	NetOut       int64     `json:"netOut"`
	
	// 负载
	Load1        float64   `json:"load1" gorm:"type:decimal(10,2)"`
	Load5        float64   `json:"load5" gorm:"type:decimal(10,2)"`
	Load15       float64   `json:"load15" gorm:"type:decimal(10,2)"`
	
	// 运行时间
	Uptime       int64     `json:"uptime"`
	
	// 进程数
	ProcessCount int       `json:"processCount"`
	
	// 连接数
	TcpCount     int       `json:"tcpCount"`
	UdpCount     int       `json:"udpCount"`
}

// NodeReport 节点上报数据
type NodeReport struct {
	// 认证信息
	UUID     string `json:"uuid"`
	Token    string `json:"token"`
	
	// 系统信息（首次上报时更新）
	OS            string `json:"os,omitempty"`
	Arch          string `json:"arch,omitempty"`
	KernelVersion string `json:"kernelVersion,omitempty"`
	CPUName       string `json:"cpuName,omitempty"`
	CPUCores      int    `json:"cpuCores,omitempty"`
	MemTotal      int64  `json:"memTotal,omitempty"`
	DiskTotal     int64  `json:"diskTotal,omitempty"`
	IPv4          string `json:"ipv4,omitempty"`
	IPv6          string `json:"ipv6,omitempty"`
	
	// 实时数据
	CPUUsage  float64 `json:"cpuUsage"`
	MemUsed   int64   `json:"memUsed"`
	SwapUsed  int64   `json:"swapUsed,omitempty"`
	SwapTotal int64   `json:"swapTotal,omitempty"`
	DiskUsed  int64   `json:"diskUsed"`
	NetIn     int64   `json:"netIn"`
	NetOut    int64   `json:"netOut"`
	Load1     float64 `json:"load1"`
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	Uptime    int64   `json:"uptime"`
	ProcessCount int  `json:"processCount,omitempty"`
	TcpCount  int     `json:"tcpCount,omitempty"`
	UdpCount  int     `json:"udpCount,omitempty"`
}

// InstallOptions 安装选项
type InstallOptions struct {
	// 平台
	Platform string `json:"platform"` // linux, windows, macos, docker
	
	// 安装选项
	DisableRemoteControl bool   `json:"disableRemoteControl,omitempty"`
	DisableAutoUpdate    bool   `json:"disableAutoUpdate,omitempty"`
	IgnoreInsecureCert   bool   `json:"ignoreInsecureCert,omitempty"`
	IncludeBufferMemory  bool   `json:"includeBufferMemory,omitempty"`
	GetIPFromNIC         bool   `json:"getIPFromNIC,omitempty"`
	EnableDetailedGPU    bool   `json:"enableDetailedGPU,omitempty"`
	GitHubProxy          string `json:"githubProxy,omitempty"`
	InstallDir           string `json:"installDir,omitempty"`
	ServiceName          string `json:"serviceName,omitempty"`
	
	// 网络监控配置
	MonitorOnlyNIC       string `json:"monitorOnlyNIC,omitempty"`
	ExcludeNIC           string `json:"excludeNIC,omitempty"`
	MonitorOnlyMount     string `json:"monitorOnlyMount,omitempty"`
	CollectInterval      int    `json:"collectInterval,omitempty"`
	MonthlyResetDay      int    `json:"monthlyResetDay,omitempty"`
}
