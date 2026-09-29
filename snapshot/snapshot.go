package snapshot

// Entry 是快照中的一个键值对。
type Entry struct {
	Key   string
	Value string
}

// Snapshot 是一份按键严格升序、键唯一的只读有序快照。
type Snapshot []Entry

// Op 表示变更类型。
type Op string

const (
	OpInsert Op = "insert"
	OpDelete Op = "delete"
	OpUpdate Op = "update"
)

// Change 是变更日志中的一条记录。
type Change struct {
	Op    Op
	Key   string
	Value string
}

// ChangeLog 是按键升序、每键至多一条的变更序列。
type ChangeLog []Change

// Validate 校验快照是否按键严格升序且无重复键。
// 从前往后扫描，第一处违规决定原因：相等为重复键，逆序为未排序。
func Validate(s Snapshot) error {
	for i := 1; i < len(s); i++ {
		switch {
		case s[i].Key == s[i-1].Key:
			return &KeyError{Kind: ErrDuplicateKey, Side: "", Index: i, Key: s[i].Key}
		case s[i].Key < s[i-1].Key:
			return &KeyError{Kind: ErrNotSorted, Side: "", Index: i, Key: s[i].Key}
		}
	}
	return nil
}

// Diff 以双指针归并两份有序快照，产出按键升序的变更日志。
func Diff(oldSnap, newSnap Snapshot, maxChanges int, log Logger) (ChangeLog, error) {
	return nil, nil
}

// Replay 把变更日志按序应用到旧快照，返回与日志应用后等价的有序快照。
func Replay(base Snapshot, log ChangeLog) (Snapshot, error) {
	return nil, nil
}
