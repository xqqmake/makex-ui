package job

import (
	"x-ui/logger"
	"x-ui/web/service"
)

// CheckSingBoxRunningJob 双内核守护：sing-box 崩溃后自动拉起。
// 与 CheckXrayRunningJob 同模式：连续 2 次检测到未运行才重启。
type CheckSingBoxRunningJob struct {
	singboxService service.SingBoxService

	checkTime int
}

func NewCheckSingBoxRunningJob() *CheckSingBoxRunningJob {
	return new(CheckSingBoxRunningJob)
}

func (j *CheckSingBoxRunningJob) Run() {
	if !j.singboxService.DidSingBoxCrash() {
		j.checkTime = 0
	} else {
		j.checkTime++
		// only restart if it's down 2 times in a row
		if j.checkTime > 1 {
			err := j.singboxService.RestartSingBox(false)
			j.checkTime = 0
			if err != nil {
				logger.Error("Restart sing-box failed:", err)
			}
		}
	}
}
