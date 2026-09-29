package membership

// Status 是成员的视图状态。
type Status uint8

const (
	// Alive 存活。
	Alive Status = iota + 1
	// Suspect 可疑。
	Suspect
	// Dead 确认失效，不可逆。
	Dead
)

func (s Status) String() string {
	switch s {
	case Alive:
		return "ALIVE"
	case Suspect:
		return "SUSPECT"
	case Dead:
		return "DEAD"
	default:
		return "UNKNOWN"
	}
}

// Message 是（成员，状态，化身号）三元组消息。
type Message struct {
	Member      string
	Status      Status
	Incarnation int64
}

// ViewItem 是某个成员当前合并后的视图。
type ViewItem struct {
	Member      string
	Status      Status
	Incarnation int64
}

// entry 是单个成员的内部视图，Deadline 仅对 Suspect 有意义。
type entry struct {
	status      Status
	incarnation int64
	deadline    int64
}

// pending 是等待捎带传播的更新。
type pending struct {
	msg       Message
	sentCount int
}

// reason 描述一次合并/拒绝判定的依据，供日志使用。
type reason string
