// Package meeting 实现在线会议室的发言权控制服务。
//
// 核心语义：
//   - 举手排队（容量受限的 FIFO），发言权同一时刻至多授予一人；
//   - 发言限时 S 秒，到期收回是惰性的：任何通过参数与时钟检查的操作
//     在执行自身逻辑之前，先按时间顺序处理全部已到期发言权并顺延；
//   - 所有操作在互斥锁下串行化，并发调用等价于某个串行顺序；
//   - 相同操作序列重放得到完全相同的结果（无墙钟、无全局随机源）。
package meeting

// Role 成员角色。任何时刻室内恰有一名 RoleHost。
type Role int

const (
	RoleAttendee Role = iota // 与会者
	RoleCoHost               // 协管员
	RoleHost                 // 主持人
)

func (r Role) String() string {
	switch r {
	case RoleHost:
		return "host"
	case RoleCoHost:
		return "cohost"
	case RoleAttendee:
		return "attendee"
	}
	return "unknown"
}

// MemberInfo 成员快照项。
type MemberInfo struct {
	User  string
	Role  Role
	Muted bool
}

// Snapshot 会议室在某时刻的一致性快照。
type Snapshot struct {
	Now           int64        // 本次快照使用的时刻（惰性处理之后）
	Closed        bool         // 会议室是否已关闭
	Speaker       string       // 当前发言者，空串表示无人发言
	RemainingSecs int64        // 发言者剩余秒数，无人发言时为 0
	Queue         []string     // 举手队列，从队首到队尾
	Members       []MemberInfo // 全部成员，按加入先后排序
}

const (
	// MaxNow 是 now 参数的合法上界（含）。
	MaxNow = int64(1_000_000_000_000)
	// MinSpeakSecs / MaxSpeakSecs 是单次发言时限 S 的合法范围。
	MinSpeakSecs = int64(1)
	MaxSpeakSecs = int64(3600)
	// MinQueueCap / MaxQueueCap 是举手队列容量 Q 的合法范围。
	MinQueueCap = 1
	MaxQueueCap = 500
)
