package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"x-ui/database"
	"x-ui/database/model"
	"x-ui/web/nodeagent"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type NodeService struct{}

// GetAllNodes 获取所有节点
func (s *NodeService) GetAllNodes() ([]*model.Node, error) {
	db := database.GetDB()
	var nodes []*model.Node
	err := db.Model(&model.Node{}).Find(&nodes).Error
	return nodes, err
}

// GetNodeById 根据ID获取节点
func (s *NodeService) GetNodeById(id int) (*model.Node, error) {
	db := database.GetDB()
	var node model.Node
	err := db.Model(&model.Node{}).Where("id = ?", id).First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// GetNodeByUUID 根据UUID获取节点
func (s *NodeService) GetNodeByUUID(uuid string) (*model.Node, error) {
	db := database.GetDB()
	var node model.Node
	err := db.Model(&model.Node{}).Where("uuid = ?", uuid).First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// CreateNode 创建节点
func (s *NodeService) CreateNode(node *model.Node) error {
	db := database.GetDB()
	
	// 生成UUID和Token
	node.UUID = uuid.New().String()
	node.Token = generateToken()
	
	// 设置默认值
	if node.Status == "" {
		node.Status = "offline"
	}
	if node.Protocol == "" {
		node.Protocol = "ssh"
	}
	if node.Port == 0 {
		node.Port = 22
	}
	
	return db.Create(node).Error
}

// UpdateNode 更新节点
func (s *NodeService) UpdateNode(node *model.Node) error {
	db := database.GetDB()
	return db.Save(node).Error
}

// DeleteNode 删除节点
func (s *NodeService) DeleteNode(id int) error {
	db := database.GetDB()
	
	// 删除节点记录
	err := db.Where("node_id = ?", id).Delete(&model.NodeRecord{}).Error
	if err != nil {
		return err
	}
	
	// 删除节点
	return db.Delete(&model.Node{}, id).Error
}

// UpdateNodeStatus 更新节点状态
func (s *NodeService) UpdateNodeStatus(uuid string, status string) error {
	db := database.GetDB()
	return db.Model(&model.Node{}).Where("uuid = ?", uuid).Update("status", status).Error
}

// SaveNodeReport 保存节点上报数据
func (s *NodeService) SaveNodeReport(report *model.NodeReport) error {
	db := database.GetDB()
	
	// 查找节点
	node, err := s.GetNodeByUUID(report.UUID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("节点不存在: %s", report.UUID)
		}
		return err
	}
	
	// 验证Token
	if node.Token != report.Token {
		return fmt.Errorf("Token验证失败")
	}
	
	// 更新节点系统信息（首次上报时）
	if report.OS != "" && node.OS == "" {
		node.OS = report.OS
	}
	if report.Arch != "" && node.Arch == "" {
		node.Arch = report.Arch
	}
	if report.KernelVersion != "" && node.KernelVersion == "" {
		node.KernelVersion = report.KernelVersion
	}
	if report.CPUName != "" && node.CPUName == "" {
		node.CPUName = report.CPUName
	}
	if report.CPUCores > 0 && node.CPUCores == 0 {
		node.CPUCores = report.CPUCores
	}
	if report.MemTotal > 0 && node.MemTotal == 0 {
		node.MemTotal = report.MemTotal
	}
	if report.DiskTotal > 0 && node.DiskTotal == 0 {
		node.DiskTotal = report.DiskTotal
	}
	if report.IPv4 != "" && node.IPv4 == "" {
		node.IPv4 = report.IPv4
	}
	if report.IPv6 != "" && node.IPv6 == "" {
		node.IPv6 = report.IPv6
	}
	
	// 更新节点状态
	node.Status = "online"
	node.LastOnline = time.Now()
	node.LastReport = time.Now()
	node.Host = report.IPv4
	
	// 保存节点信息
	if err := db.Save(node).Error; err != nil {
		return err
	}
	
	// 保存监控记录
	record := &model.NodeRecord{
		NodeId:       node.Id,
		UUID:         report.UUID,
		Time:         time.Now(),
		CPUUsage:     report.CPUUsage,
		MemUsed:      report.MemUsed,
		MemTotal:     report.MemTotal,
		SwapUsed:     report.SwapUsed,
		SwapTotal:    report.SwapTotal,
		DiskUsed:     report.DiskUsed,
		DiskTotal:    report.DiskTotal,
		NetIn:        report.NetIn,
		NetOut:       report.NetOut,
		Load1:        report.Load1,
		Load5:        report.Load5,
		Load15:       report.Load15,
		Uptime:       report.Uptime,
		ProcessCount: report.ProcessCount,
		TcpCount:     report.TcpCount,
		UdpCount:     report.UdpCount,
	}
	
	return db.Create(record).Error
}

// GetNodeRecords 获取节点监控记录
func (s *NodeService) GetNodeRecords(nodeId int, limit int) ([]*model.NodeRecord, error) {
	db := database.GetDB()
	var records []*model.NodeRecord
	err := db.Model(&model.NodeRecord{}).
		Where("node_id = ?", nodeId).
		Order("time DESC").
		Limit(limit).
		Find(&records).Error
	return records, err
}

// GetNodeRecordsByTimeRange 获取指定时间范围的节点监控记录
func (s *NodeService) GetNodeRecordsByTimeRange(nodeId int, start, end time.Time) ([]*model.NodeRecord, error) {
	db := database.GetDB()
	var records []*model.NodeRecord
	err := db.Model(&model.NodeRecord{}).
		Where("node_id = ? AND time BETWEEN ? AND ?", nodeId, start, end).
		Order("time ASC").
		Find(&records).Error
	return records, err
}

// CleanOldRecords 清理旧的监控记录
func (s *NodeService) CleanOldRecords(days int) error {
	db := database.GetDB()
	return db.Where("time < ?", time.Now().AddDate(0, 0, -days)).Delete(&model.NodeRecord{}).Error
}

// GetOnlineNodeCount 获取在线节点数
func (s *NodeService) GetOnlineNodeCount() (int64, error) {
	db := database.GetDB()
	var count int64
	err := db.Model(&model.Node{}).Where("status = ?", "online").Count(&count).Error
	return count, err
}

// GetTotalNodeCount 获取总节点数
func (s *NodeService) GetTotalNodeCount() (int64, error) {
	db := database.GetDB()
	var count int64
	err := db.Model(&model.Node{}).Count(&count).Error
	return count, err
}

// GetNodeByToken 根据Token获取节点（agent连接鉴权）
func (s *NodeService) GetNodeByToken(token string) (*model.Node, error) {
	db := database.GetDB()
	var node model.Node
	err := db.Model(&model.Node{}).Where("token = ?", token).First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// SaveV2Report 保存 v2 协议上报数据（komari-agent 格式）
func (s *NodeService) SaveV2Report(uuid string, report *nodeagent.Report) error {
	db := database.GetDB()

	node, err := s.GetNodeByUUID(uuid)
	if err != nil {
		return err
	}

	now := time.Now()

	// 更新节点状态与实时系统信息
	node.Status = "online"
	node.LastOnline = now
	node.LastReport = now

	if report.CPU.Name != "" {
		node.CPUName = report.CPU.Name
	}
	if report.CPU.Cores > 0 {
		node.CPUCores = report.CPU.Cores
	}
	if report.CPU.Arch != "" && node.Arch == "" {
		node.Arch = report.CPU.Arch
	}
	if report.Ram.Total > 0 {
		node.MemTotal = report.Ram.Total
	}
	if report.Disk.Total > 0 {
		node.DiskTotal = report.Disk.Total
	}

	if err := db.Save(node).Error; err != nil {
		return err
	}

	// 保存监控记录
	record := &model.NodeRecord{
		NodeId:    node.Id,
		UUID:      uuid,
		Time:      now,
		CPUUsage:  report.CPU.Usage,
		MemUsed:   report.Ram.Used,
		MemTotal:  report.Ram.Total,
		SwapUsed:  report.Swap.Used,
		SwapTotal: report.Swap.Total,
		DiskUsed:  report.Disk.Used,
		DiskTotal: report.Disk.Total,
		NetIn:     report.Network.Down,
		NetOut:    report.Network.Up,
		Load1:     report.Load.Load1,
		Load5:     report.Load.Load5,
		Load15:    report.Load.Load15,
		Uptime:    report.Uptime,
		ProcessCount: report.Process,
		TcpCount:  report.Connections.TCP,
		UdpCount:  report.Connections.UDP,
	}

	return db.Create(record).Error
}

// SaveV2BasicInfo 保存 v2 协议基本信息（komari-agent 格式）
func (s *NodeService) SaveV2BasicInfo(uuid string, info map[string]interface{}) error {
	db := database.GetDB()

	node, err := s.GetNodeByUUID(uuid)
	if err != nil {
		return err
	}

	if v, ok := info["cpu_name"].(string); ok && v != "" {
		node.CPUName = v
	}
	if v, ok := info["cpu_cores"].(float64); ok && v > 0 {
		node.CPUCores = int(v)
	}
	if v, ok := info["arch"].(string); ok && v != "" {
		node.Arch = v
	}
	if v, ok := info["os"].(string); ok && v != "" {
		node.OS = v
	}
	if v, ok := info["kernel_version"].(string); ok && v != "" {
		node.KernelVersion = v
	}
	if v, ok := info["ipv4"].(string); ok && v != "" {
		node.IPv4 = v
		node.Host = v
	}
	if v, ok := info["ipv6"].(string); ok && v != "" {
		node.IPv6 = v
	}
	if v, ok := info["mem_total"].(float64); ok && v > 0 {
		node.MemTotal = int64(v)
	}
	if v, ok := info["disk_total"].(float64); ok && v > 0 {
		node.DiskTotal = int64(v)
	}

	return db.Save(node).Error
}

// generateToken 生成32字节的随机Token
func generateToken() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
