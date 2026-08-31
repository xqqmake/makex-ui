package nodeagent

// 连接管理与事件队列（从 komari-monitor/komari web/agent 移植，MIT License）
// - connectedClients: WS 长连接（uuid -> conn）
// - latestReport: 最新上报的内存缓存（供状态接口实时返回）
// - v2EventQueues: 离线事件队列（agent 不在线时缓存指令，重连后补发）

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	v2EventQueueLimit = 128
	v2EventTTL        = 5 * time.Minute
)

// SafeConn 包装 WebSocket 连接，提供线程安全的读写
type SafeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{conn: conn}
}

func (s *SafeConn) WriteJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *SafeConn) WriteMessage(messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(messageType, data)
}

func (s *SafeConn) ReadJSON(v any) error {
	return s.conn.ReadJSON(v)
}

func (s *SafeConn) Close() error {
	return s.conn.Close()
}

// v2EventQueue agent 的离线事件队列
type v2EventQueue struct {
	events []Event
	signal chan struct{}
}

var (
	connectedClients = make(map[string]*SafeConn)
	latestReport     = make(map[string]*Report)
	onlineTimestamps = make(map[string]time.Time) // 最近一次收到 agent 消息的时间

	mu           sync.RWMutex
	v2EventMu    sync.Mutex
	v2EventQueues = make(map[string]*v2EventQueue)
)

// SetConnectedClient 注册在线连接
func SetConnectedClient(uuid string, conn *SafeConn) {
	mu.Lock()
	defer mu.Unlock()
	connectedClients[uuid] = conn
	onlineTimestamps[uuid] = time.Now()
}

// GetConnectedClient 获取在线连接
func GetConnectedClient(uuid string) *SafeConn {
	mu.RLock()
	defer mu.RUnlock()
	return connectedClients[uuid]
}

// DeleteConnectedClient 删除连接（仅当是同一个 conn 时）
func DeleteConnectedClient(uuid string, conn *SafeConn) {
	mu.Lock()
	defer mu.Unlock()
	if cur, ok := connectedClients[uuid]; ok && cur == conn {
		delete(connectedClients, uuid)
	}
}

// IsOnline 判断节点是否在线
func IsOnline(uuid string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := connectedClients[uuid]
	return ok
}

// GetAllOnlineUUIDs 获取所有在线节点 UUID
func GetAllOnlineUUIDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	res := make([]string, 0, len(connectedClients))
	for k := range connectedClients {
		res = append(res, k)
	}
	return res
}

// TouchOnline 更新在线时间戳（agent 每次上报/拉取时调用）
func TouchOnline(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	onlineTimestamps[uuid] = time.Now()
}

// RecordReport 更新内存中的最新上报缓存
func RecordReport(report Report) {
	if report.UUID == "" {
		return
	}
	if report.UpdatedAt.IsZero() {
		report.UpdatedAt = time.Now().UTC()
	}
	mu.Lock()
	defer mu.Unlock()
	if latest := latestReport[report.UUID]; latest == nil || !report.UpdatedAt.Before(latest.UpdatedAt) {
		item := report
		latestReport[report.UUID] = &item
	}
}

// GetLatestReport 获取某节点最新上报
func GetLatestReport(uuid string) *Report {
	mu.RLock()
	defer mu.RUnlock()
	r := latestReport[uuid]
	if r == nil {
		return nil
	}
	item := *r
	return &item
}

// GetAllLatestReports 获取所有节点最新上报
func GetAllLatestReports() map[string]*Report {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]*Report, len(latestReport))
	for k, v := range latestReport {
		if v == nil {
			continue
		}
		item := *v
		out[k] = &item
	}
	return out
}

// DeleteLatestReport 删除节点缓存（删除节点时调用）
func DeleteLatestReport(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	delete(latestReport, uuid)
	delete(onlineTimestamps, uuid)
}

// ---------- 事件队列 ----------

func getV2EventQueueLocked(uuid string) *v2EventQueue {
	q := v2EventQueues[uuid]
	if q == nil {
		q = &v2EventQueue{signal: make(chan struct{})}
		v2EventQueues[uuid] = q
	}
	return q
}

// DispatchV2Event 下发指令：在线直发，离线入队（重连后补发）
func DispatchV2Event(uuid, method string, params any) bool {
	conn := GetConnectedClient(uuid)
	if conn != nil {
		payload := Request{JSONRPC: Version, Method: method, Params: params}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	EnqueueV2Event(uuid, method, params)
	return true
}

// EnqueueV2Event 入队事件
func EnqueueV2Event(uuid, method string, params any) Event {
	event := Event{
		ID:        newV2EventID(),
		Method:    method,
		Params:    params,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(v2EventTTL),
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	q := getV2EventQueueLocked(uuid)
	coalesceV2EventLocked(q, event)
	pruneExpiredV2EventsLocked(q)
	select {
	case q.signal <- struct{}{}:
	default:
	}
	return event
}

func newV2EventID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// coalesceV2EventLocked 合并同类事件（如 exec 同一 task 重复入队）
func coalesceV2EventLocked(q *v2EventQueue, event Event) {
	for i := range q.events {
		if q.events[i].Method == event.Method && sameParams(q.events[i].Params, event.Params) {
			q.events[i] = event
			return
		}
	}
	if len(q.events) >= v2EventQueueLimit {
		// 队列满，丢弃最旧
		q.events = q.events[1:]
	}
	q.events = append(q.events, event)
}

func sameParams(a, b any) bool {
	ab, err1 := jsonMarshal(a)
	bb, err2 := jsonMarshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func pruneExpiredV2EventsLocked(q *v2EventQueue) {
	now := time.Now().UTC()
	kept := q.events[:0]
	for _, e := range q.events {
		if e.ExpiresAt.After(now) {
			kept = append(kept, e)
		}
	}
	q.events = kept
}

// AckV2Events 确认事件已处理（从队列移除）
func AckV2Events(uuid string, ackIDs []string) {
	if len(ackIDs) == 0 {
		return
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	q := v2EventQueues[uuid]
	if q == nil {
		return
	}
	ackSet := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		ackSet[id] = struct{}{}
	}
	kept := q.events[:0]
	for _, e := range q.events {
		if _, ok := ackSet[e.ID]; !ok {
			kept = append(kept, e)
		}
	}
	q.events = kept
}

// TakeV2Events 取出待补发事件
func TakeV2Events(uuid string, ackIDs []string, limit int) []Event {
	if limit <= 0 {
		limit = v2EventQueueLimit
	}
	AckV2Events(uuid, ackIDs)
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	q := v2EventQueues[uuid]
	if q == nil {
		return []Event{}
	}
	pruneExpiredV2EventsLocked(q)
	if len(q.events) == 0 {
		delete(v2EventQueues, uuid)
		return []Event{}
	}
	if len(q.events) > limit {
		q.events = q.events[:limit]
	}
	out := append([]Event(nil), q.events...)
	q.events = nil
	delete(v2EventQueues, uuid)
	return out
}

// WaitV2Events 阻塞等待新事件（agent pull 长轮询用）
func WaitV2Events(uuid string, ackIDs []string, timeout time.Duration) []Event {
	events := TakeV2Events(uuid, ackIDs, 0)
	if len(events) > 0 {
		return events
	}
	v2EventMu.Lock()
	q := v2EventQueues[uuid]
	v2EventMu.Unlock()
	if q == nil {
		return []Event{}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-q.signal:
		return TakeV2Events(uuid, nil, 0)
	case <-timer.C:
		return []Event{}
	}
}
