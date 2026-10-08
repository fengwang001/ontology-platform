package snapshot

// volume 是一个卷的内部状态。
type volume struct {
	id  string
	seq uint64 // 已应用写入数，即下一个写入将分配的写序号（初始 0，应用后加一）

	// capacity 是冻结排队容量；queue 预分配该容量，入队为严格 O(1)。
	capacity int
	queue    []string

	group *group // nil 表示不属于任何组

	// confirmed 仅在组处于冻结中阶段时有意义：该卷是否已确认冻结。
	confirmed bool
}

func newVolume(id string, capacity int) *volume {
	return &volume{id: id, capacity: capacity, queue: make([]string, 0, capacity)}
}

// needsQueue 判定此刻写入该卷是否需要排队。
// 只读取三个字段（组指针、组阶段、本卷确认标志），不遍历成员或队列，
// 开销不随组内卷数与已排队写入数增长，可验证为常数。
func (v *volume) needsQueue() bool {
	g := v.group
	return g != nil && (g.phase == PhaseFrozen || (g.phase == PhaseFreezing && v.confirmed))
}

// enqueueing 是 needsQueue 的对外视图别名。
func (v *volume) enqueueing() bool { return v.needsQueue() }

func (v *volume) view() VolumeView {
	groupID := ""
	if v.group != nil {
		groupID = v.group.id
	}
	queue := make([]string, len(v.queue))
	copy(queue, v.queue)
	return VolumeView{
		ID:         v.id,
		Seq:        v.seq,
		GroupID:    groupID,
		Enqueueing: v.enqueueing(),
		Queue:      queue,
	}
}
