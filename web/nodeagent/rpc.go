package nodeagent

// v2 RPC 处理（从 komari-monitor/komari web/api/client 移植，MIT License）
// - HandleV2RPC: agent WS 长连接（升级 + 循环读请求 + 补发排队事件）
// - UploadV2RPC: agent POST 上报（兼容 gzip）
// - 上报数据通过回调写回数据库（由 controller 注入）

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"x-ui/logger"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	// 面板可能有 base_path(/xui)，任何 Origin 都放行
}

// RPCHandlers 上报处理回调（由 NodeController 注入数据库读写逻辑）
type RPCHandlers struct {
	OnReport     func(uuid string, report *Report) error
	OnBasicInfo  func(uuid string, info map[string]interface{}) error
	OnTaskResult func(uuid string, result *TaskResultParams) error
	OnTerminal   func(uuid string, requestID string) error
}

var rpcHandlers *RPCHandlers

// SetRPCHandlers 注册处理回调
func SetRPCHandlers(h *RPCHandlers) {
	rpcHandlers = h
}

// HandleV2RPC agent WebSocket 长连接入口
// 路径: /api/clients/v2/rpc?token=xxx&uuid=xxx
func HandleV2RPC(w http.ResponseWriter, r *http.Request, uuid string) {
	if !IsWebSocketUpgrade(r) {
		http.Error(w, "Require WebSocket upgrade", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Infof("nodeagent: WS upgrade error: %v", err)
		return
	}
	safeConn := NewSafeConn(conn)
	SetConnectedClient(uuid, safeConn)
	TouchOnline(uuid)
	logger.Infof("nodeagent: agent %s connected (WS)", uuid)

	// 上线后立即推送排队事件
	go pushQueuedV2Events(uuid, safeConn)

	defer func() {
		DeleteConnectedClient(uuid, safeConn)
		conn.Close()
		logger.Infof("nodeagent: agent %s disconnected (WS)", uuid)
	}()

	for {
		var req Request
		if err := safeConn.ReadJSON(&req); err != nil {
			// 正常关闭或异常断开
			return
		}
		TouchOnline(uuid)
		handleAgentRequest(uuid, safeConn, &req)
	}
}

// UploadV2RPC agent POST 上报入口（单次请求，非长连接）
func UploadV2RPC(w http.ResponseWriter, r *http.Request, uuid string) {
	body := r.Body
	defer body.Close()

	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			writeJSON(w, Error(nil, -32700, "gzip read error: "+err.Error(), nil))
			return
		}
		defer gz.Close()
		body = gz
	}

	data, err := io.ReadAll(body)
	if err != nil {
		writeJSON(w, Error(nil, -32700, "read body error: "+err.Error(), nil))
		return
	}

	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		// 兼容数组批量请求
		var reqs []Request
		if err2 := json.Unmarshal(data, &reqs); err2 != nil {
			writeJSON(w, Error(nil, -32700, "invalid JSON-RPC: "+err.Error(), nil))
			return
		}
		results := make([]any, 0, len(reqs))
		for i := range reqs {
			results = append(results, handleAgentRequestSync(&reqs[i], uuid))
		}
		writeJSON(w, results)
		return
	}

	TouchOnline(uuid)
	resp := handleAgentRequestSync(&req, uuid)
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// handleAgentRequest 处理单条请求（长连接内）
func handleAgentRequest(uuid string, conn *SafeConn, req *Request) {
	// 先做 ack 处理（report/pull 都携带 ack_event_ids）
	if req.Method == MethodAgentReport {
		var params ReportParams
		if err := bindParams(req.Params, &params); err == nil && len(params.AckEventIDs) > 0 {
			AckV2Events(uuid, params.AckEventIDs)
		}
	}
	if req.Method == MethodAgentPull {
		var params PullParams
		if err := bindParams(req.Params, &params); err == nil && len(params.AckEventIDs) > 0 {
			AckV2Events(uuid, params.AckEventIDs)
		}
	}

	resp := handleAgentRequestSync(req, uuid)
	// 长连接内需要回响应
	if r, ok := resp.(Response); ok && (r.ID != nil || r.Error != nil) {
		conn.WriteJSON(r)
	}
}

// handleAgentRequestSync 处理单条请求，返回响应（POST 与 WS 共用）
func handleAgentRequestSync(req *Request, uuid string) any {
	switch req.Method {
	case MethodAgentReport:
		var params ReportParams
		if err := bindParams(req.Params, &params); err != nil {
			return Error(req.ID, -32602, "invalid report params: "+err.Error(), nil)
		}
		if rpcHandlers != nil && rpcHandlers.OnReport != nil {
			report := params.Report
			if report.UUID == "" {
				report.UUID = uuid // agent 上报不携带 uuid，以 token 解析结果为准
			}
			if err := rpcHandlers.OnReport(report.UUID, &report); err != nil {
				return Error(req.ID, -32000, "save report error: "+err.Error(), nil)
			}
		}
		return Success(req.ID, "ok")

	case MethodAgentBasicInfo:
		var params BasicInfoParams
		if err := bindParams(req.Params, &params); err != nil {
			return Error(req.ID, -32602, "invalid basicInfo params: "+err.Error(), nil)
		}
		if rpcHandlers != nil && rpcHandlers.OnBasicInfo != nil {
			if err := rpcHandlers.OnBasicInfo(uuid, params.Info); err != nil {
				return Error(req.ID, -32000, "save basicInfo error: "+err.Error(), nil)
			}
		}
		return Success(req.ID, "ok")

	case MethodAgentTaskResult:
		var params TaskResultParams
		if err := bindParams(req.Params, &params); err != nil {
			return Error(req.ID, -32602, "invalid taskResult params: "+err.Error(), nil)
		}
		if rpcHandlers != nil && rpcHandlers.OnTaskResult != nil {
			if err := rpcHandlers.OnTaskResult(uuid, &params); err != nil {
				return Error(req.ID, -32000, "save taskResult error: "+err.Error(), nil)
			}
		}
		return Success(req.ID, "ok")

	case MethodAgentTerminal:
		var params TerminalRequestParams
		if err := bindParams(req.Params, &params); err != nil {
			return Error(req.ID, -32602, "invalid terminal params: "+err.Error(), nil)
		}
		if rpcHandlers != nil && rpcHandlers.OnTerminal != nil {
			if err := rpcHandlers.OnTerminal(uuid, params.RequestID); err != nil {
				return Error(req.ID, -32000, "terminal attach error: "+err.Error(), nil)
			}
		}
		return Success(req.ID, "ok")

	case MethodAgentPull:
		var params PullParams
		if err := bindParams(req.Params, &params); err != nil {
			return Error(req.ID, -32602, "invalid pull params: "+err.Error(), nil)
		}
		return Success(req.ID, map[string]any{"events": []any{}})

	default:
		return Error(req.ID, -32601, "method not found: "+req.Method, nil)
	}
}

// pushQueuedV2Events 上线后推送离线排队事件
func pushQueuedV2Events(uuid string, conn *SafeConn) {
	events := TakeV2Events(uuid, nil, 0)
	if len(events) == 0 {
		return
	}
	payload := Request{
		JSONRPC: Version,
		Method:  MethodAgentPull,
		Params:  map[string]any{"events": events},
	}
	if err := conn.WriteJSON(payload); err != nil {
		logger.Infof("nodeagent: push queued events to %s error: %v", uuid, err)
	}
}

// IsWebSocketUpgrade 判断是否为 WebSocket 升级请求
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

var _ = time.Second // 保留 time 引入（供后续扩展）
