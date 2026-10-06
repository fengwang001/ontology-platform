// Package medschedule 实现住院护理给药时间表系统。
//
// 子模块职责：
//   - errors.go   可区分错误类型与优先级
//   - schedule.go 计划点数学（固定间隔 / 固定时点 / 补给重排分段）
//   - system.go   系统状态、时钟、目录、患者与并发控制
//   - orders.go   医嘱开立、停嘱、改嘱、给药/拒服/补给/PRN
//   - query.go    区间查询
//   - naive.go    独立朴素参考模型（测试对照用）
package medschedule

// Status 是计划点的处理/生命周期状态。
type Status int

const (
	StatusPending     Status = iota // 待给（窗口内可登记，或未来的计划点）
	StatusGivenOnTime               // 已给（按时窗口内登记）
	StatusMadeUp                    // 已补给（漏给后补给）
	StatusRefused                   // 拒服
	StatusMissed                    // 漏给
	StatusVoid                      // 作废（停嘱/改嘱/重排截断）
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusGivenOnTime:
		return "GIVEN_ON_TIME"
	case StatusMadeUp:
		return "MADE_UP"
	case StatusRefused:
		return "REFUSED"
	case StatusMissed:
		return "MISSED"
	case StatusVoid:
		return "VOID"
	}
	return "UNKNOWN"
}

// Point 是查询返回的计划点快照。
type Point struct {
	OrderID string
	Patient string
	Drug    string
	Time    int64 // 计划时刻（秒）
	Status  Status
}

// PRNDose 是必要时给药的一次实际给药记录。
type PRNDose struct {
	OrderID string
	Patient string
	Drug    string
	Time    int64
}
