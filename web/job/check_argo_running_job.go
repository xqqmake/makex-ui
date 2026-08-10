package job

import (
	"x-ui/logger"
	"x-ui/web/service"
)

// CheckArgoRunningJob Argo 守护：cloudflared 隧道崩溃后自动拉起。
// 与 CheckXrayRunningJob 同模式：连续 2 次检测到未运行才重启。
type CheckArgoRunningJob struct {
	argoService service.ArgoService

	checkTime int
}

func NewCheckArgoRunningJob() *CheckArgoRunningJob {
	return new(CheckArgoRunningJob)
}

func (j *CheckArgoRunningJob) Run() {
	if !j.argoService.DidArgoCrash() {
		j.checkTime = 0
	} else {
		j.checkTime++
		// only restart if it's down 2 times in a row
		if j.checkTime > 1 {
			err := j.argoService.RestartArgo()
			j.checkTime = 0
			if err != nil {
				logger.Error("Restart argo tunnel failed:", err)
			}
		}
	}
}
