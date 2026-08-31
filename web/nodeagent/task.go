package nodeagent

// exec 任务结果存储：agent 执行完命令后通过 agent.taskResult 上报，
// 浏览器端轮询 /panel/api/nodes/:id/agent/task/:taskId 获取结果。

import (
	"sync"
	"time"
)

// TaskResult 任务执行结果
type TaskResult struct {
	TaskID     string    `json:"task_id"`
	UUID       string    `json:"uuid"` // 节点 uuid
	Result     string    `json:"result"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
	Done       bool      `json:"done"`
}

var (
	taskMu       sync.RWMutex
	taskResults  = make(map[string]*TaskResult)
	taskTTL      = 10 * time.Minute
)

// StoreTaskResult 保存任务结果（由 OnTaskResult 回调调用）
func StoreTaskResult(uuid string, r *TaskResultParams) {
	taskMu.Lock()
	defer taskMu.Unlock()
	taskResults[r.TaskID] = &TaskResult{
		TaskID:     r.TaskID,
		UUID:       uuid,
		Result:     r.Result,
		ExitCode:   r.ExitCode,
		FinishedAt: r.FinishedAt,
		Done:       true,
	}
	// 惰性清理过期任务
	if len(taskResults) > 500 {
		now := time.Now()
		for k, v := range taskResults {
			if now.Sub(v.FinishedAt) > taskTTL {
				delete(taskResults, k)
			}
		}
	}
}

// GetTaskResult 获取任务结果（未完成返回 nil）
func GetTaskResult(taskID string) *TaskResult {
	taskMu.RLock()
	defer taskMu.RUnlock()
	r := taskResults[taskID]
	if r == nil {
		return nil
	}
	item := *r
	return &item
}

// NewTaskID 生成任务 ID
func NewTaskID() string {
	return randomHex(16)
}

// ExecTask 下发执行命令任务，返回 taskID
func ExecTask(uuid string, command string) string {
	taskID := NewTaskID()
	DispatchV2Event(uuid, MethodAgentExec, ExecParams{TaskID: taskID, Command: command})
	return taskID
}
