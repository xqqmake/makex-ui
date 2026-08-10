package job

import (
	"x-ui/logger"
	"x-ui/web/service"
)

// SingBoxTrafficJob periodically pulls traffic counters from the
// sing-box core (anytls/tuic/naive inbounds) and writes them into the
// panel database, mirroring XrayTrafficJob for the xray core.
type SingBoxTrafficJob struct {
	inboundService service.InboundService
	singboxService service.SingBoxService
}

func NewSingBoxTrafficJob() *SingBoxTrafficJob {
	return new(SingBoxTrafficJob)
}

func (j *SingBoxTrafficJob) Run() {
	if !j.singboxService.IsSingBoxRunning() {
		return
	}
	traffics, clientTraffics, err := j.singboxService.GetSingBoxTraffic()
	if err != nil {
		return
	}
	err, needRestart := j.inboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("add sing-box inbound traffic failed:", err)
	}
	if needRestart {
		service.MarkSingBoxNeedRestart()
	}
}
