package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"x-ui/database/model"
	"x-ui/util/crypto"
	"x-ui/web/nodeagent"
	"x-ui/web/service"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type NodeController struct {
	BaseController
	nodeService service.NodeService
	sshDialer   service.SSHDialer
}

func NewNodeController(g *gin.RouterGroup) *NodeController {
	ctl := &NodeController{}
	ctl.initRouter(g)
	return ctl
}

func (c *NodeController) initRouter(g *gin.RouterGroup) {
	api := g.Group("/panel/api")
	
	// 需要认证的路由
	authGroup := api.Group("/nodes")
	authGroup.Use(c.checkLogin)
	authGroup.GET("/list", c.getNodes)
	authGroup.GET("/:id", c.getNode)
	authGroup.POST("/create", c.createNode)
	authGroup.POST("/update", c.updateNode)
	authGroup.DELETE("/:id", c.deleteNode)
	authGroup.GET("/:id/status", c.getNodeStatus)
	authGroup.GET("/:id/records", c.getNodeRecords)
	authGroup.GET("/:id/install-cmd", c.getInstallCommand)
	authGroup.POST("/:id/exec", c.execCommand)
	authGroup.GET("/:id/ws", c.sshTerminal)

	// 【Agent 远程控制】需要认证的路由（浏览器侧）
	authGroup.GET("/agent/online", c.getAgentOnlineNodes)          // 批量在线状态
	authGroup.GET("/:id/agent/terminal", c.browserTerminal)        // 浏览器终端 WS
	authGroup.POST("/:id/agent/exec", c.execAgentCommand)          // 下发命令
	authGroup.GET("/:id/agent/task/:taskId", c.getAgentTaskResult) // 轮询任务结果
	authGroup.GET("/:id/agent/status", c.getAgentStatus)           // 实时状态
	authGroup.GET("/:id/agent/kill", c.killAgentTerminal)          // 关闭终端会话

	// 无需认证的路由（安装脚本和上报）
	publicGroup := api.Group("/nodes")
	publicGroup.GET("/install.sh", c.getInstallScript)
	publicGroup.POST("/report", c.handleReport)

	// 【Agent 远程控制】agent 侧公开路由（token 鉴权，与 komari-agent 兼容）
	// agent 连接地址 = 面板地址 + /api/clients/v2/rpc?token=xxx
	agentGroup := g.Group("/api/clients")
	agentGroup.GET("/v2/rpc", c.agentV2RPC)    // agent WS 长连接
	agentGroup.POST("/v2/rpc", c.agentV2RPC)   // agent POST 上报
	agentGroup.GET("/terminal", c.agentTerminal) // agent 终端 WS
	// v1 遗留端点（komari-agent release 1.2.60 及更早版本仍在使用）
	agentGroup.POST("/task/result", c.agentV1TaskResult)      // 任务结果上传
	agentGroup.POST("/uploadBasicInfo", c.agentV1BasicInfo)   // 基础信息上传
	agentGroup.Any("/report", c.agentV1Report)                // v1 上报(WS/POST)

	// 注册 v2 RPC 回调（只注册一次）
	nodeagent.SetRPCHandlers(&nodeagent.RPCHandlers{
		OnReport: func(uuid string, report *nodeagent.Report) error {
			nodeagent.RecordReport(*report)
			return c.nodeService.SaveV2Report(uuid, report)
		},
		OnBasicInfo: func(uuid string, info map[string]interface{}) error {
			return c.nodeService.SaveV2BasicInfo(uuid, info)
		},
		OnTaskResult: func(uuid string, result *nodeagent.TaskResultParams) error {
			nodeagent.StoreTaskResult(uuid, result)
			return nil
		},
		OnTerminal: func(uuid string, requestID string) error {
			return nil
		},
	})
}

// getNodes 获取所有节点
func (c *NodeController) getNodes(g *gin.Context) {
	nodes, err := c.nodeService.GetAllNodes()
	if err != nil {
		jsonMsg(g, "获取节点列表失败", err)
		return
	}
	// 隐藏SSH密码（不返回明文和密文）
	for _, n := range nodes {
		n.SSHPassword = ""
		n.Token = ""
	}
	jsonObj(g, nodes, nil)
}

// getNode 获取单个节点
func (c *NodeController) getNode(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	node.SSHPassword = ""
	node.Token = ""
	jsonObj(g, node, nil)
}

// createNode 创建节点
func (c *NodeController) createNode(g *gin.Context) {
	var node model.Node
	// 前端 axios 拦截器会把 data 转为 form-urlencoded，必须用 ShouldBind（自动兼容 form/JSON）
	if err := g.ShouldBind(&node); err != nil {
		jsonMsg(g, "参数错误", err)
		return
	}
	// 加密SSH密码
	if node.SSHPassword != "" {
		enc, err := crypto.EncryptSecret(node.SSHPassword)
		if err != nil {
			jsonMsg(g, "加密SSH密码失败", err)
			return
		}
		node.SSHPassword = enc
	}
	if err := c.nodeService.CreateNode(&node); err != nil {
		jsonMsg(g, "创建节点失败", err)
		return
	}
	node.SSHPassword = ""
	jsonMsgObj(g, "创建成功", node, nil)
}

// updateNode 更新节点
func (c *NodeController) updateNode(g *gin.Context) {
	var node model.Node
	// 前端 axios 拦截器会把 data 转为 form-urlencoded，必须用 ShouldBind（自动兼容 form/JSON）
	if err := g.ShouldBind(&node); err != nil {
		jsonMsg(g, "参数错误", err)
		return
	}
	// 读取数据库中的现有节点（避免Save全量覆盖清空未提交字段）
	old, err := c.nodeService.GetNodeById(node.Id)
	if err != nil {
		jsonMsg(g, "节点不存在", err)
		return
	}
	// 如果密码字段非空则加密更新，否则保留原密码
	if node.SSHPassword != "" {
		enc, err := crypto.EncryptSecret(node.SSHPassword)
		if err != nil {
			jsonMsg(g, "加密SSH密码失败", err)
			return
		}
		old.SSHPassword = enc
	}
	// 合并更新：只覆盖请求中提供的字段
	old.Name = node.Name
	old.Remark = node.Remark
	old.Group = node.Group
	old.SSHUser = node.SSHUser
	if err := c.nodeService.UpdateNode(old); err != nil {
		jsonMsg(g, "更新节点失败", err)
		return
	}
	old.SSHPassword = ""
	jsonMsgObj(g, "更新成功", old, nil)
}

// deleteNode 删除节点
func (c *NodeController) deleteNode(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	if err := c.nodeService.DeleteNode(id); err != nil {
		jsonMsg(g, "删除节点失败", err)
		return
	}
	jsonMsg(g, "删除成功", nil)
}

// getNodeStatus 获取节点状态
func (c *NodeController) getNodeStatus(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	records, err := c.nodeService.GetNodeRecords(id, 1)
	if err != nil {
		jsonMsg(g, "获取记录失败", err)
		return
	}
	var record *model.NodeRecord
	if len(records) > 0 {
		record = records[0]
	}
	jsonObj(g, gin.H{
		"node":   node,
		"record": record,
	}, nil)
}

// getNodeRecords 获取节点监控记录
func (c *NodeController) getNodeRecords(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	limitStr := g.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	records, err := c.nodeService.GetNodeRecords(id, limit)
	if err != nil {
		jsonMsg(g, "获取记录失败", err)
		return
	}
	jsonObj(g, records, nil)
}

// handleReport 处理从节点上报
func (c *NodeController) handleReport(g *gin.Context) {
	var report model.NodeReport
	if err := g.ShouldBindJSON(&report); err != nil {
		jsonMsg(g, "参数错误", err)
		return
	}
	if err := c.nodeService.SaveNodeReport(&report); err != nil {
		jsonMsg(g, "上报失败", err)
		return
	}
	jsonMsg(g, "上报成功", nil)
}

// getInstallScript 返回安装脚本
func (c *NodeController) getInstallScript(g *gin.Context) {
	g.Header("Content-Type", "text/x-shellscript")
	g.Header("Content-Disposition", "attachment; filename=\"install-node.sh\"")
	g.File("/usr/local/x-ui/web/html/install-node.sh")
}

// getInstallCommand 生成节点安装命令（Komari 风格一键部署）
func (c *NodeController) getInstallCommand(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	
	// 获取安装选项
	platform := g.DefaultQuery("platform", "linux")
	interval := g.DefaultQuery("interval", "30")
	githubProxy := g.Query("github_proxy")
	monitorOnlyNIC := g.Query("monitor_only_nic")
	excludeNIC := g.Query("exclude_nic")
	monitorOnlyMount := g.Query("monitor_only_mount")
	monthlyResetDay := g.DefaultQuery("monthly_reset_day", "1")
	installDir := g.Query("install_dir")
	serviceName := g.Query("service_name")
	
	// 布尔选项
	disableRemoteControl := g.Query("disable_remote_control") == "true"
	disableAutoUpdate := g.Query("disable_auto_update") == "true"
	ignoreInsecureCert := g.Query("ignore_insecure_cert") == "true"
	includeBufferMemory := g.Query("include_buffer_memory") == "true"
	getIPFromNIC := g.Query("get_ip_from_nic") == "true"
	enableDetailedGPU := g.Query("enable_detailed_gpu") == "true"
	
	// 获取主控地址（agent 会自动拼接 /api/clients/v2/rpc）
	scheme := "https"
	if g.Request.TLS == nil {
		scheme = "http"
	}
	masterHost := g.Request.Host
	masterURL := fmt.Sprintf("%s://%s%s", scheme, masterHost, g.GetString("base_path"))
	
	// agent 参数（install.sh 中 --install-* 由脚本消费，其余原样传给 agent 二进制）
	agentArgs := ""
	if disableRemoteControl {
		agentArgs += " --disable-web-ssh"
	}
	if disableAutoUpdate {
		agentArgs += " --disable-auto-update"
	}
	if ignoreInsecureCert {
		agentArgs += " --ignore-unsafe-cert"
	}
	if includeBufferMemory {
		agentArgs += " --memory-include-cache"
	}
	if getIPFromNIC {
		agentArgs += " --get-ip-addr-from-nic"
	}
	if enableDetailedGPU {
		agentArgs += " --gpu"
	}
	if interval != "" && interval != "30" {
		agentArgs += " --interval " + interval
	}
	if monitorOnlyNIC != "" {
		agentArgs += " --include-nics '" + monitorOnlyNIC + "'"
	}
	if excludeNIC != "" {
		agentArgs += " --exclude-nics '" + excludeNIC + "'"
	}
	if monitorOnlyMount != "" {
		agentArgs += " --include-mountpoint '" + monitorOnlyMount + "'"
	}
	if monthlyResetDay != "" && monthlyResetDay != "1" {
		agentArgs += " --month-rotate " + monthlyResetDay
	}
	
	// install.sh 参数
	installArgs := ""
	if githubProxy != "" {
		installArgs += " --install-ghproxy '" + githubProxy + "'"
	}
	if installDir != "" {
		installArgs += " --install-dir '" + installDir + "'"
	}
	if serviceName != "" {
		installArgs += " --install-service-name '" + serviceName + "'"
	}
	
	// 构建安装命令
	var cmd string
	
	switch platform {
	case "linux", "macos":
		cmd = fmt.Sprintf("wget -qO- https://raw.githubusercontent.com/komari-monitor/komari-agent/refs/heads/main/install.sh | sudo bash -s --%s -e '%s' -t '%s'%s",
			installArgs, masterURL, node.Token, agentArgs)
		
	case "windows":
		cmd = fmt.Sprintf("wget -qO- https://raw.githubusercontent.com/komari-monitor/komari-agent/refs/heads/main/install.sh | bash -s --%s -e '%s' -t '%s'%s  # Windows (Git Bash/WSL)",
			installArgs, masterURL, node.Token, agentArgs)
		
	case "docker":
		cmd = fmt.Sprintf("docker run -d --name komari-agent --restart always ghcr.io/komari-monitor/komari-agent:latest -e '%s' -t '%s'%s",
			masterURL, node.Token, agentArgs)
	}
	
	// 解析安装选项
	installOpts := model.InstallOptions{
		Platform:             platform,
		DisableRemoteControl: disableRemoteControl,
		DisableAutoUpdate:    disableAutoUpdate,
		IgnoreInsecureCert:   ignoreInsecureCert,
		IncludeBufferMemory:  includeBufferMemory,
		GetIPFromNIC:         getIPFromNIC,
		EnableDetailedGPU:    enableDetailedGPU,
		GitHubProxy:          githubProxy,
		MonitorOnlyNIC:       monitorOnlyNIC,
		ExcludeNIC:           excludeNIC,
		MonitorOnlyMount:     monitorOnlyMount,
		CollectInterval:      intervalInt(interval),
		MonthlyResetDay:      intervalInt(monthlyResetDay),
		InstallDir:           installDir,
		ServiceName:          serviceName,
	}
	
	// 保存安装选项到节点
	optsJSON, _ := json.Marshal(installOpts)
	node.InstallOptions = string(optsJSON)
	c.nodeService.UpdateNode(node)
	
	jsonObj(g, gin.H{
		"command": cmd,
		"uuid":    node.UUID,
		"token":   node.Token,
		"master":  masterURL,
	}, nil)
}

func intervalInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// execCommand 通过SSH在节点上执行命令
func (c *NodeController) execCommand(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}

	var req struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"` // 秒，默认10
	}
	if err := g.ShouldBindJSON(&req); err != nil {
		jsonMsg(g, "参数错误", err)
		return
	}
	if req.Command == "" {
		jsonMsg(g, "命令不能为空", nil)
		return
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if req.Timeout <= 0 || req.Timeout > 120 {
		timeout = 10 * time.Second
	}

	stdout, stderr, err := c.sshDialer.ExecCommand(node, req.Command, timeout)
	if err != nil {
		jsonObj(g, gin.H{
			"success": false,
			"stdout":  stdout,
			"stderr":  stderr,
			"error":   err.Error(),
		}, nil)
		return
	}
	jsonObj(g, gin.H{
		"success": true,
		"stdout":  stdout,
		"stderr":  stderr,
	}, nil)
}

// sshTerminal WebSocket交互式SSH终端
func (c *NodeController) sshTerminal(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}

	// 升级为WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	wsConn, err := upgrader.Upgrade(g.Writer, g.Request, nil)
	if err != nil {
		jsonMsg(g, "WebSocket升级失败", err)
		return
	}
	defer wsConn.Close()

	// 建立SSH连接
	client, err := c.sshDialer.BuildSSHClient(node, 15*time.Second)
	if err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[SSH连接失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[会话创建失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer session.Close()

	// 请求PTY（交互式终端）
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	cols, rows := 120, 32
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[PTY请求失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[stdin管道失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[stdout管道失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[stderr管道失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	if err := session.Shell(); err != nil {
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[Shell启动失败] "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	// 关闭信号
	done := make(chan struct{})

	// SSH输出 → WebSocket
	go func() {
		buf := make([]byte, 4096)
		multi := io.MultiReader(stdoutPipe, stderrPipe)
		for {
			n, err := multi.Read(buf)
			if n > 0 {
				if werr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[33m[连接已关闭]\x1b[0m\r\n"))
				close(done)
				return
			}
		}
	}()

	// WebSocket输入 → SSH stdin
	go func() {
		for {
			_, data, err := wsConn.ReadMessage()
			if err != nil {
				session.Close()
				return
			}
			if len(data) == 0 {
				continue
			}
			// 处理窗口调整指令
			if data[0] == 0xFF && len(data) > 2 {
				// 自定义协议: 0xFF 0x01 rows cols
				if data[1] == 0x01 && len(data) >= 4 {
					r := int(data[2])
					cl := int(data[3])
					session.WindowChange(r, cl)
					continue
				}
			}
			if _, err := stdinPipe.Write(data); err != nil {
				return
			}
		}
	}()

	// 等待关闭或会话结束
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		wsConn.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[33m[终端超时，连接已关闭]\x1b[0m\r\n"))
	}
}

// ==================== Agent 远程控制（komari-agent 兼容 v2 RPC） ====================

// agentV2RPC agent 连接入口（WS 长连接 / POST 上报，token 鉴权）
func (c *NodeController) agentV2RPC(g *gin.Context) {
	token := g.Query("token")
	if token == "" {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing token"})
		return
	}
	node, err := c.nodeService.GetNodeByToken(token)
	if err != nil {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid token"})
		return
	}
	if nodeagent.IsWebSocketUpgrade(g.Request) {
		nodeagent.HandleV2RPC(g.Writer, g.Request, node.UUID)
	} else {
		nodeagent.UploadV2RPC(g.Writer, g.Request, node.UUID)
	}
}

// agentV1TaskResult v1 任务结果上传（komari-agent release 1.2.60 及更早）
// body: {"task_id":"...","result":"...","exit_code":0,"finished_at":"..."}
func (c *NodeController) agentV1TaskResult(g *gin.Context) {
	token := g.Query("token")
	if token == "" {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing token"})
		return
	}
	node, err := c.nodeService.GetNodeByToken(token)
	if err != nil {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid token"})
		return
	}
	var params nodeagent.TaskResultParams
	if err := g.ShouldBindJSON(&params); err != nil {
		g.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid body: " + err.Error()})
		return
	}
	nodeagent.StoreTaskResult(node.UUID, &params)
	g.JSON(http.StatusOK, gin.H{"status": "success"})
}

// agentV1BasicInfo v1 基础信息上传
// body: {"hostname":"...","os":"...","ipv4":"...", ...}
func (c *NodeController) agentV1BasicInfo(g *gin.Context) {
	token := g.Query("token")
	if token == "" {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing token"})
		return
	}
	node, err := c.nodeService.GetNodeByToken(token)
	if err != nil {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid token"})
		return
	}
	var info map[string]interface{}
	if err := g.ShouldBindJSON(&info); err != nil {
		g.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid body: " + err.Error()})
		return
	}
	if err := c.nodeService.SaveV2BasicInfo(node.UUID, info); err != nil {
		g.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": err.Error()})
		return
	}
	g.JSON(http.StatusOK, gin.H{"status": "success"})
}

// agentV1Report v1 上报入口（兼容旧版 agent 的 WS/POST 上报）
func (c *NodeController) agentV1Report(g *gin.Context) {
	token := g.Query("token")
	if token == "" {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing token"})
		return
	}
	node, err := c.nodeService.GetNodeByToken(token)
	if err != nil {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid token"})
		return
	}
	if nodeagent.IsWebSocketUpgrade(g.Request) {
		nodeagent.HandleV2RPC(g.Writer, g.Request, node.UUID)
		return
	}
	nodeagent.UploadV2RPC(g.Writer, g.Request, node.UUID)
}

// agentTerminal agent 端终端 WS 连入（token + request_id 鉴权）
func (c *NodeController) agentTerminal(g *gin.Context) {
	token := g.Query("token")
	if token == "" {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "missing token"})
		return
	}
	node, err := c.nodeService.GetNodeByToken(token)
	if err != nil {
		g.JSON(http.StatusUnauthorized, gin.H{"status": "error", "error": "invalid token"})
		return
	}
	requestID := g.Query("id")
	if requestID == "" {
		g.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "missing id"})
		return
	}
	nodeagent.EstablishConnection(g.Writer, g.Request, requestID, node.UUID)
}

// browserTerminal 浏览器端终端 WS（面板登录鉴权）
func (c *NodeController) browserTerminal(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	if !nodeagent.RequestTerminal(g.Writer, g.Request, node.UUID) {
		// RequestTerminal 内部已写入错误响应
		return
	}
}

// killAgentTerminal 关闭指定节点的所有终端会话
func (c *NodeController) killAgentTerminal(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	nodeagent.CloseAllSessionsForNode(node.UUID)
	jsonMsg(g, "终端已关闭", nil)
}

// execAgentCommand 下发命令到 agent 执行（异步，返回 taskId）
func (c *NodeController) execAgentCommand(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := g.ShouldBindJSON(&req); err != nil {
		jsonMsg(g, "参数错误", err)
		return
	}
	if req.Command == "" {
		jsonMsg(g, "命令不能为空", nil)
		return
	}

	if !nodeagent.IsOnline(node.UUID) {
		jsonMsg(g, "节点离线", nil)
		return
	}
	taskID := nodeagent.ExecTask(node.UUID, req.Command)
	jsonObj(g, gin.H{
		"taskId": taskID,
		"online": true,
	}, nil)
}

// getAgentTaskResult 轮询命令执行结果
func (c *NodeController) getAgentTaskResult(g *gin.Context) {
	taskID := g.Param("taskId")
	if taskID == "" {
		jsonMsg(g, "无效的任务ID", nil)
		return
	}
	result := nodeagent.GetTaskResult(taskID)
	if result == nil {
		jsonObj(g, gin.H{"done": false}, nil)
		return
	}
	jsonObj(g, result, nil)
}

// getAgentStatus 节点实时状态（在线 + 最新上报数据）
func (c *NodeController) getAgentStatus(g *gin.Context) {
	id, err := strconv.Atoi(g.Param("id"))
	if err != nil {
		jsonMsg(g, "无效的节点ID", err)
		return
	}
	node, err := c.nodeService.GetNodeById(id)
	if err != nil {
		jsonMsg(g, "获取节点失败", err)
		return
	}
	node.SSHPassword = ""
	node.Token = ""
	report := nodeagent.GetLatestReport(node.UUID)
	online := nodeagent.IsOnline(node.UUID)
	jsonObj(g, gin.H{
		"online": online,
		"node":   node,
		"report": report,
	}, nil)
}

// getAgentOnlineNodes 批量在线状态（返回在线 uuid 列表 + 最新上报）
func (c *NodeController) getAgentOnlineNodes(g *gin.Context) {
	online := nodeagent.GetAllOnlineUUIDs()
	reports := nodeagent.GetAllLatestReports()
	jsonObj(g, gin.H{
		"online":  online,
		"reports": reports,
	}, nil)
}
