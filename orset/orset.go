// Package orset implements an observed-remove set CRDT.
//
// 观察删除集合（OR-Set）：每个元素的每次添加都生成唯一标签，
// 删除只移除该元素当前全部存活标签（墓碑记录标签而非元素名）。
// 元素属于集合当且仅当它至少有一个未进入墓碑的存活标签。
package orset

// Tag 是一次添加的唯一标识：(副本编号, 该副本内单调递增的序号)。
type Tag struct {
	Replica int
	Seq     int64
}

// ErrCode 标识一次被拒绝操作的错误类别。
type ErrCode int

const (
	ErrOK ErrCode = iota
)

// Error 是带可区分错误类别的拒绝原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

// Replica 是 OR-Set 的一个副本。
type Replica struct {
}

// NewReplica 创建编号为 id 的副本，maxAdds 为添加记录数上限。
func NewReplica(id int, maxAdds int) (*Replica, error) {
	return nil, nil
}

// Add 添加元素并返回本次添加的唯一标签。
func (r *Replica) Add(elem string) (Tag, error) {
	return Tag{}, nil
}

// Remove 删除元素当前全部存活标签；元素不存在则拒绝。
func (r *Replica) Remove(elem string) error {
	return nil
}

// Merge 把 src 的添加记录与墓碑并入目标副本 r，仅修改 r。
func (r *Replica) Merge(src *Replica) error {
	return nil
}

// Contains 判定元素当前是否在集合中。
func (r *Replica) Contains(elem string) (bool, error) {
	return false, nil
}

// Elements 返回集合中全部元素，结果按字典序排序、可复现。
func (r *Replica) Elements() []string {
	return nil
}

// Snapshot 返回用于日志与自检的确定性状态快照。
type Snapshot struct {
	ReplicaID int
	Seq       int64
	Adds      map[string][]Tag
	Tombstone []Tag
	Elements  []string
}

// Snapshot 返回当前副本的状态快照。
func (r *Replica) Snapshot() Snapshot {
	return Snapshot{}
}

// Check 对副本做内部一致性自检，返回发现的问题描述（空串表示健康）。
func (r *Replica) Check() string {
	return ""
}

// SetLogger 配置操作日志输出；nil 表示关闭日志。
func (r *Replica) SetLogger(w interface{}) {
}
