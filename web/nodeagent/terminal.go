package nodeagent

// 终端转发（从 komari-monitor/komari web/api/terminal 移植，MIT License）
// 流程:
//   浏览器 WS 连入 -> 生成 request_id -> DispatchV2Event(agent.terminal.request)
//   agent 收到后 WS 连入 /api/clients/terminal?id=<request_id>
//   maybeStartForwarding 将 browser conn 与 agent conn 双向桥接（二进制帧原样转发）

import (
	"crypto/rand"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TerminalSession 一次终端会话（浏览器端 conn + agent 端 conn）
type TerminalSession struct {
	ID           string
	UUID         string // 节点 uuid
	UserUUID     string // 发起浏览器用户（makex-ui 用 session 鉴权，留空即可）
	Browser      *SafeConn
	Agent        *SafeConn
	RequesterIp  string
	LastActivity time.Time
}

var (
	TerminalSessions      = make(map[string]*TerminalSession)
	TerminalSessionsMutex sync.RWMutex
)

const terminalSessionTTL = 30 * time.Minute

// maybeStartForwarding 两端都连上后启动双向转发
func maybeStartForwarding(sessionID string) {
	TerminalSessionsMutex.RLock()
	session := TerminalSessions[sessionID]
	TerminalSessionsMutex.RUnlock()
	if session == nil || session.Browser == nil || session.Agent == nil {
		return
	}
	ForwardTerminal(session)
}

// ForwardTerminal 双向转发：browser <-> agent
func ForwardTerminal(session *TerminalSession) {
	browser := session.Browser
	agent := session.Agent

	// browser -> agent
	go func() {
		for {
			_, data, err := browser.conn.ReadMessage()
			if err != nil {
				return
			}
			if agent.WriteMessage(websocket.BinaryMessage, data) != nil {
				return
			}
		}
	}()

	// agent -> browser
	go func() {
		for {
			_, data, err := agent.conn.ReadMessage()
			if err != nil {
				return
			}
			if browser.WriteMessage(websocket.BinaryMessage, data) != nil {
				return
			}
		}
	}()
}

// attachBrowser 浏览器端（重新）连接会话
func attachBrowser(id string, userUUID string, conn *SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil {
		return nil, false
	}
	session.Browser = conn
	session.LastActivity = time.Now()
	return session, true
}

// attachAgent agent 端连接会话
func attachAgent(id string, conn *SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil {
		return nil, false
	}
	session.Agent = conn
	session.LastActivity = time.Now()
	return session, true
}

// suspendSession 一端断开时挂起（保留会话，等待重连）
func suspendSession(id string, browserConn, agentConn *SafeConn) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil {
		return
	}
	if browserConn != nil && session.Browser == browserConn {
		session.Browser = nil
	}
	if agentConn != nil && session.Agent == agentConn {
		session.Agent = nil
	}
}

// closeSession 关闭并删除会话
func closeSession(id string) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil {
		return
	}
	if session.Browser != nil {
		session.Browser.Close()
	}
	if session.Agent != nil {
		session.Agent.Close()
	}
	delete(TerminalSessions, id)
}

// scheduleCleanup 定时清理过期会话
func scheduleCleanup(id string, session *TerminalSession) {
	go func() {
		time.Sleep(terminalSessionTTL)
		TerminalSessionsMutex.Lock()
		cur := TerminalSessions[id]
		TerminalSessionsMutex.Unlock()
		if cur == session {
			closeSession(id)
		}
	}()
}

// EstablishConnection agent 端连入终端会话
// 路径: /api/clients/terminal?token=xxx&id=<request_id>
func EstablishConnection(w http.ResponseWriter, r *http.Request, requestID string, uuid string) {
	TerminalSessionsMutex.RLock()
	session, exists := TerminalSessions[requestID]
	TerminalSessionsMutex.RUnlock()
	if !exists || session == nil || session.Browser == nil {
		http.Error(w, `{"status":"error","error":"Session not found"}`, http.StatusNotFound)
		return
	}
	if session.UUID != "" && session.UUID != uuid {
		http.Error(w, `{"status":"error","error":"Forbidden"}`, http.StatusForbidden)
		return
	}
	if !IsWebSocketUpgrade(r) {
		http.Error(w, "Require WebSocket upgrade", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		closeSession(requestID)
		return
	}
	safeConn := NewSafeConn(conn)
	_, ok := attachAgent(requestID, safeConn)
	if !ok {
		conn.Close()
		return
	}
	conn.SetCloseHandler(func(code int, text string) error {
		suspendSession(requestID, nil, safeConn)
		return nil
	})
	maybeStartForwarding(requestID)
}

// RequestTerminal 浏览器端请求终端会话
// 路径: /panel/api/nodes/:id/agent/terminal?request_id=<可选，重连>
// 返回 WS，首帧为 {"request_id":"xxx"}
func RequestTerminal(w http.ResponseWriter, r *http.Request, uuid string) bool {
	id := r.URL.Query().Get("request_id")
	userID, _ := r.Context().Value("user_id").(string)

	if id != "" {
		// 重连已有会话
		TerminalSessionsMutex.RLock()
		session := TerminalSessions[id]
		TerminalSessionsMutex.RUnlock()
		if session == nil || session.UUID != uuid {
			return false
		}
		if !IsWebSocketUpgrade(r) {
			http.Error(w, "Require WebSocket upgrade", http.StatusBadRequest)
			return false
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return false
		}
		safeConn := NewSafeConn(conn)
		s, ok := attachBrowser(id, userID, safeConn)
		if !ok || s.UUID != uuid {
			conn.WriteMessage(websocket.TextMessage, []byte("Terminal session expired\n终端会话已过期\n"))
			conn.Close()
			return false
		}
		conn.SetCloseHandler(func(code int, text string) error {
			suspendSession(id, safeConn, nil)
			return nil
		})
		conn.WriteJSON(map[string]string{"request_id": id})
		if !DispatchV2Event(uuid, MethodAgentTerminal, TerminalRequestParams{RequestID: id}) {
			conn.WriteMessage(websocket.TextMessage, []byte("Client offline!\n被控端离线!\n"))
			closeSession(id)
			return false
		}
		if s.Agent == nil {
			conn.WriteMessage(websocket.TextMessage, []byte("等待被控端连接 waiting for agent...\n"))
		}
		maybeStartForwarding(id)
		return true
	}

	// 新建终端会话
	if !IsWebSocketUpgrade(r) {
		http.Error(w, "Require WebSocket upgrade", http.StatusBadRequest)
		return false
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return false
	}
	safeConn := NewSafeConn(conn)
	id = randomHex(16)
	session := &TerminalSession{
		ID:           id,
		UserUUID:     userID,
		UUID:         uuid,
		Browser:      safeConn,
		Agent:        nil,
		RequesterIp:  r.RemoteAddr,
		LastActivity: time.Now(),
	}
	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = session
	scheduleCleanup(id, session)
	TerminalSessionsMutex.Unlock()
	conn.SetCloseHandler(func(code int, text string) error {
		suspendSession(id, safeConn, nil)
		return nil
	})
	conn.WriteJSON(map[string]string{"request_id": id})
	if !DispatchV2Event(uuid, MethodAgentTerminal, TerminalRequestParams{RequestID: id}) {
		conn.WriteMessage(websocket.TextMessage, []byte("Client offline!\n被控端离线!\n"))
		conn.Close()
		closeSession(id)
		return false
	}
	conn.WriteMessage(websocket.TextMessage, []byte("等待被控端连接 waiting for agent...\n"))
	return true
}

// randomHex 生成随机十六进制字符串
func randomHex(n int) string {
	const chars = "0123456789abcdef"
	b := make([]byte, n*2)
	randBytes := make([]byte, n*2)
	io.ReadFull(rand.Reader, randBytes)
	for i := range b {
		b[i] = chars[int(randBytes[i])%len(chars)]
	}
	return string(b)
}

// CloseAllSessionsForNode 节点删除时关闭其所有终端会话
func CloseAllSessionsForNode(uuid string) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	for id, session := range TerminalSessions {
		if session.UUID == uuid {
			if session.Browser != nil {
				session.Browser.Close()
			}
			if session.Agent != nil {
				session.Agent.Close()
			}
			delete(TerminalSessions, id)
		}
	}
}
