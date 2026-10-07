package lab

// AppItem 是一条申请项：一次申请中针对一个检验项目的待办。
// 同一患者同一项目同一时刻至多存在一条未终结的申请项。
type AppItem struct {
	ItemID    string
	AppID     string
	PatientID string
	Priority  int // 继承自原申请，重采不变

	spec ItemSpec // 提交时刻的目录快照，判定一律以快照为准

	Status       ItemStatus
	Rejections   int
	LastReject   RejectReason
	PendingSince int64  // 最近一次进入待采集的时刻
	CollectTime  int64  // 本次采集时刻（仅待签收时有效）
	TubeID       string // 所在管（仅待签收时有效）
}

// Patient 聚合一名患者的全部未终结申请项。
// 只保留未终结项目，使查询开销与历史项目总数无关。
type Patient struct {
	ID       string
	items    map[string]*AppItem   // 仅未终结；key 为项目标识
	finished map[string]ItemStatus // 已终结项目的最终状态，用于区分“对象不存在”与“状态不符”
}

func newPatient(id string) *Patient {
	return &Patient{
		ID:       id,
		items:    make(map[string]*AppItem),
		finished: make(map[string]ItemStatus),
	}
}

// finish 将申请项移入终结记录。
func (p *Patient) finish(it *AppItem, status ItemStatus) {
	it.Status = status
	delete(p.items, it.ItemID)
	p.finished[it.ItemID] = status
}
