package compensate

// branchPhase 是分支运行期状态。
type branchPhase int

const (
	bpPending branchPhase = iota
	bpRunning
	bpApplied
	bpFailedSelf
	bpFailedUpstream
	bpCompensating
	bpCompensated
	bpCompensateFailed
)
