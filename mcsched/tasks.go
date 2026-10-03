package mcsched

// MaxTasks 为任务表容量上限。
const MaxTasks = 16

const (
	maxPeriod = 1000
	maxPhase  = 1_000_000
	maxIDLen  = 32
)

// validTask 检查静态参数合法性：编号非空且不超过 32 字节；关键级为 LO/HI；
// CL>=1；LO 任务 CH==CL，HI 任务 CL<=CH；1<=T<=1000 且 CH<=T；
// prio 为正整数；0<=phi<=10^6。
func validTask(t Task) bool {
	if len(t.ID) == 0 || len(t.ID) > maxIDLen {
		return false
	}
	if t.Lvl != LO && t.Lvl != HI {
		return false
	}
	if t.CL < 1 {
		return false
	}
	switch t.Lvl {
	case LO:
		if t.CH != t.CL {
			return false
		}
	case HI:
		if t.CH < t.CL {
			return false
		}
	}
	if t.T < 1 || t.T > maxPeriod || t.CH > t.T {
		return false
	}
	if t.Prio < 1 {
		return false
	}
	if t.Phi < 0 || t.Phi > maxPhase {
		return false
	}
	return true
}
