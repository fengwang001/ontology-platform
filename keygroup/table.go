package keygroup

import "sync"

// Migration 描述一次扩缩容中单个键组的迁移记录。
type Migration struct {
	Group int // 键组编号
	From  int // 原实例下标
	To    int // 新实例下标
	Keys  int // 该键组内被迁移的键数
}

// RescaleReport 汇总一次扩缩容的结果。
type RescaleReport struct {
	OldParallelism int
	NewParallelism int
	Migrations     []Migration // 仅包含归属发生变化的键组
	MigratedKeys   int         // 被移动的键总数
	TotalKeys      int         // 扩缩容后键总数（与扩缩容前一致）
}

// Snapshot 是状态表在某一时刻的一致性快照，可脱离锁安全读取。
type Snapshot struct {
	MaxParallelism int
	Parallelism    int
	Assignment     []KeyGroupRange // 每个实例的键组区间
	GroupOwner     []int           // 每个键组归属的实例下标
	States         map[string]string
}

// Table 维护按键存储的状态及其到实例的键组映射。
// 所有方法可并发调用；读取返回一致性快照。
type Table struct {
	mu             sync.RWMutex
	maxParallelism int
	parallelism    int
	assignment     []KeyGroupRange
	groupOwner     []int
	states         map[string]string
}

// NewTable 创建状态表。参数非法时返回错误且不产生任何状态。
func NewTable(maxParallelism, parallelism int) (*Table, error) {
	assignment, err := ComputeKeyGroupAssignment(maxParallelism, parallelism)
	if err != nil {
		return nil, err
	}
	return &Table{
		maxParallelism: maxParallelism,
		parallelism:    parallelism,
		assignment:     assignment,
		groupOwner:     ownersOf(assignment, maxParallelism),
		states:         make(map[string]string),
	}, nil
}

// Put 写入键值。空键被拒绝且不改变表。
func (t *Table) Put(key, value string) error {
	if _, err := KeyToGroup(key, t.maxParallelism); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states[key] = value
	return nil
}

// Delete 删除键。空键被拒绝且不改变表。
func (t *Table) Delete(key string) error {
	if _, err := KeyToGroup(key, t.maxParallelism); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, key)
	return nil
}

// Snapshot 返回当前状态的一致性深拷贝快照。
func (t *Table) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	states := make(map[string]string, len(t.states))
	for k, v := range t.states {
		states[k] = v
	}
	return Snapshot{
		MaxParallelism: t.maxParallelism,
		Parallelism:    t.parallelism,
		Assignment:     append([]KeyGroupRange(nil), t.assignment...),
		GroupOwner:     append([]int(nil), t.groupOwner...),
		States:         states,
	}
}

// Rescale 将并行度调整为 newParallelism，
// 仅迁移归属变化的键组，返回迁移报告。
// 参数非法时返回错误，且并行度、键值与分桶均保持不变。
func (t *Table) Rescale(newParallelism int) (RescaleReport, error) {
	newAssignment, err := ComputeKeyGroupAssignment(t.maxParallelism, newParallelism)
	if err != nil {
		return RescaleReport{}, err
	}
	newOwner := ownersOf(newAssignment, t.maxParallelism)

	t.mu.Lock()
	defer t.mu.Unlock()

	keysPerGroup := make([]int, t.maxParallelism)
	for key := range t.states {
		group, err := KeyToGroup(key, t.maxParallelism)
		if err != nil {
			return RescaleReport{}, err
		}
		keysPerGroup[group]++
	}

	report := RescaleReport{
		OldParallelism: t.parallelism,
		NewParallelism: newParallelism,
		TotalKeys:      len(t.states),
	}
	for group := 0; group < t.maxParallelism; group++ {
		from, to := t.groupOwner[group], newOwner[group]
		if from == to {
			continue
		}
		report.Migrations = append(report.Migrations, Migration{
			Group: group,
			From:  from,
			To:    to,
			Keys:  keysPerGroup[group],
		})
		report.MigratedKeys += keysPerGroup[group]
	}

	t.parallelism = newParallelism
	t.assignment = newAssignment
	t.groupOwner = newOwner
	return report, nil
}

// ownersOf 由区间划分展开得到每个键组的归属实例。
func ownersOf(assignment []KeyGroupRange, maxParallelism int) []int {
	owners := make([]int, maxParallelism)
	for i, r := range assignment {
		for g := r.Start; g <= r.End; g++ {
			owners[g] = i
		}
	}
	return owners
}
