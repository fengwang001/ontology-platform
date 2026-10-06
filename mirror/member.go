package mirror

// MemberState 是成员状态：在线、故障、重同步中。
type MemberState int

const (
	Online MemberState = iota
	Faulted
	Resyncing
)

func (s MemberState) String() string {
	switch s {
	case Online:
		return "在线"
	case Faulted:
		return "故障"
	case Resyncing:
		return "重同步中"
	}
	return "未知"
}

// member 是卷的一个副本成员。data 为该成员的盘面数据；
// dirty 仅在非在线期间存在，在线成员不持有脏区记录。
type member struct {
	state    MemberState
	faultGen uint64 // 转为故障时卷的世代（转换前的世代）
	data     []string
	dirty    *dirtyLog
}
