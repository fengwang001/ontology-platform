package room

// Phase 为房间生命周期阶段。
type Phase string

const (
	PhaseWaiting   Phase = "waiting"   // 等待
	PhaseCountdown Phase = "countdown" // 倒计时
	PhasePlaying   Phase = "playing"   // 进行中
	PhaseSettling  Phase = "settling"  // 结算中
	PhaseEnded     Phase = "ended"     // 已结束
	PhaseVoided    Phase = "voided"    // 已作废
)

// VoidReason 描述房间作废原因。
type VoidReason string

const (
	VoidEmpty   VoidReason = "empty"   // 无人在室（等待/倒计时）
	VoidTooFew  VoidReason = "too_few" // 进行中在室玩家不足 2 人
	VoidDispute VoidReason = "dispute" // 全体上报但不一致
	VoidTimeout VoidReason = "timeout" // 上报期限到期且未满足过半一致
)

// Config 为房间创建参数。
//
// L 为人数下限 [2,20]，U 为上限 [L,20]，C 为就绪倒计时秒数 [1,600]，
// R 为结果上报期限秒数 [1,3600]。
type Config struct {
	L int
	U int
	C int64
	R int64
}

// Snapshot 为房间某一逻辑时刻（惰性到期已处理、时钟已推进后）的只读视图。
type Snapshot struct {
	Phase Phase
	Void  VoidReason
	Owner string
	Now   int64

	Present    []string
	Ready      map[string]bool
	ReadyCount int

	CountdownStart int64
	CountdownDue   int64

	StartAt  int64
	Roster   []string
	Quitters map[string]bool

	ReportDeadline int64
	Reports        map[string]string
	Winner         string
}
