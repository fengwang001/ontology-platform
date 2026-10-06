package backupretention

// Kind 区分全量与增量备份。
type Kind int

const (
	// KindFull 全量备份，没有父备份。
	KindFull Kind = iota + 1
	// KindIncremental 增量备份，必须有父备份。
	KindIncremental
)

// 上限常量（非负整数取值域）。
const (
	// MaxBackupSize 备份大小上限（字节）。
	MaxBackupSize int64 = 1<<62 - 1
	// MaxTimestamp 时刻上限（自 Unix 纪元起的 UTC 秒）。
	MaxTimestamp int64 = 1<<62 - 1
)

// Backup 是一个已登记备份的内部记录。
type Backup struct {
	ID        string
	Kind      Kind
	ParentID  string // 全量备份为空串
	Size      int64
	CreatedAt int64 // UTC 秒，登记时刻即创建时刻
	Corrupt   bool
	LegalHold bool
}

// registry 保存全部已登记备份；空 map 即合法初始状态。
type registry struct {
	byID map[string]*Backup
}

func newRegistry() *registry {
	return &registry{byID: make(map[string]*Backup)}
}

// add 登记一个备份。登记只做 map 插入，不遍历任何已有备份，
// 因此单次登记开销为 O(1)，不随已登记备份总数增长。
func (r *registry) add(b *Backup) { r.byID[b.ID] = b }

// get 返回备份指针；不存在时第二个返回值为 false。
func (r *registry) get(id string) (*Backup, bool) {
	b, ok := r.byID[id]
	return b, ok
}

// delete 移除一个备份（清理执行时调用）。
func (r *registry) delete(id string) { delete(r.byID, id) }

// len 返回已登记备份数量。
func (r *registry) len() int { return len(r.byID) }

// all 提供对全部备份的遍历；调用方不得在遍历时修改 map。
func (r *registry) all() map[string]*Backup { return r.byID }
