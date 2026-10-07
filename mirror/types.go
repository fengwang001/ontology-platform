package mirror

// MemberState 是成员的三种状态之一。
type MemberState int

const (
	// MemberOnline 在线：服务读写，数据完整。
	MemberOnline MemberState = iota
	// MemberFaulted 故障：不服务任何读写，持有脏区记录。
	MemberFaulted
	// MemberResyncing 重同步中：接受写入但不服务读，按批次补齐缺失块。
	MemberResyncing
)

func (s MemberState) String() string {
	switch s {
	case MemberOnline:
		return "在线"
	case MemberFaulted:
		return "故障"
	case MemberResyncing:
		return "重同步中"
	default:
		return "未知状态"
	}
}

// MemberSnapshot 是某一成员在某一时刻的完整可复现状态。
type MemberSnapshot struct {
	State        MemberState // 当前状态
	FaultGen     uint64      // 最近一次转为故障时卷的世代（未故障过为 0）
	Pending      []int       // 待同步块集合（升序）；在线成员为空
	DirtyDropped bool        // 脏区记录已因超限被丢弃，必须全量重同步
	Data         []uint64    // 该成员盘面上的全部块值
}

// Snapshot 是整个卷在某一时刻的完整可复现状态。
type Snapshot struct {
	Generation uint64           // 卷世代
	Members    []MemberSnapshot // 按成员编号排列
}
