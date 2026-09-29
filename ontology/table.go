package ontology

import "time"

// 错误类别（可区分、互不相同）。
var (
	ErrInvalidArgument = newSnapshotError("invalid argument")
	ErrTimestampOrder  = newSnapshotError("commit timestamp must be strictly greater than the current snapshot timestamp")
	ErrFileNameConflict = newSnapshotError("new file name must never be reused")
	ErrFileNotInSnapshot = newSnapshotError("removed file is not present in the current snapshot")
	ErrEmptyChain      = newSnapshotError("snapshot chain is empty")
)

// SnapshotError 携带稳定错误码，便于调用方区分拒绝原因。
type SnapshotError struct {
	code string
	msg  string
}

func newSnapshotError(msg string) *SnapshotError {
	return &SnapshotError{code: msg, msg: msg}
}

func (e *SnapshotError) Error() string { return e.msg }

func (e *SnapshotError) Is(target error) bool {
	other, ok := target.(*SnapshotError)
	return ok && other.code == e.code
}

// ExpirePolicy 定义一次过期操作的保留规则。
type ExpirePolicy struct {
	// RetainLatest 保留最新的若干个现存快照（按快照序号降序取前 N 个）。
	RetainLatest int
	// Threshold 非空时，保留时间戳严格大于该阈值的快照；为 nil 表示不按时间保留。
	Threshold *time.Time
}

// CommitRequest 描述一次提交及其随后执行的过期规则。
type CommitRequest struct {
	// Added 本次新增的数据文件名；文件名在全部历史中永不复用。
	Added []string
	// Removed 本次从当前快照中移除的数据文件名；必须存在于当前快照。
	Removed []string
	// Timestamp 提交时间，必须严格大于上一快照的提交时间。
	Timestamp time.Time
	// Policy 提交完成后立即执行一次过期所使用的保留规则。
	Policy ExpirePolicy
}

// CommitResult 为一次提交并过期的结果。
type CommitResult struct {
	SnapshotID int
	Retained   []int
	Expired    []int
	Deleted    []string
}

// Snapshot 描述一个现存快照。
type Snapshot struct {
	ID        int
	Timestamp time.Time
	Files     []string
	ParentID  int
}

// Table 维护快照链、文件存储与引用计数；所有方法可被并发调用。
type Table struct {
}

// NewTable 创建空表（尚无任何快照）。
func NewTable() *Table {
	return &Table{}
}

// Commit 原子地产生新快照并按规则过期旧快照。
func (t *Table) Commit(req CommitRequest) (CommitResult, error) {
	return CommitResult{}, nil
}

// Expire 按保留规则对现存快照执行一次过期。
func (t *Table) Expire(policy ExpirePolicy) (retained []int, expired []int, deleted []string, err error) {
	return nil, nil, nil, nil
}

// CurrentSnapshot 返回当前（最新）快照。
func (t *Table) CurrentSnapshot() (Snapshot, error) {
	return Snapshot{}, nil
}

// GetSnapshot 按序号返回现存快照。
func (t *Table) GetSnapshot(id int) (Snapshot, error) {
	return Snapshot{}, nil
}

// ListSnapshots 按快照序号升序返回全部现存快照。
func (t *Table) ListSnapshots() []Snapshot {
	return nil
}

// ListFiles 返回文件存储中全部数据文件名（升序）。
func (t *Table) ListFiles() []string {
	return nil
}

// RefCount 返回某数据文件当前被多少个现存快照引用。
func (t *Table) RefCount(file string) int {
	return 0
}

// Verify 执行自检。
func (t *Table) Verify() error {
	return nil
}
