package observedset

import (
	"errors"
	"strconv"
)

// 互不相同、可区分的错误类别。使用 errors.Is 即可判别具体原因。
var (
	// ErrNilReplica：操作或合并涉及的副本为 nil。
	ErrNilReplica = errors.New("observedset: replica must not be nil")
	// ErrInvalidReplicaID：副本编号为负数。
	ErrInvalidReplicaID = errors.New("observedset: replica id must be non-negative")
	// ErrEmptyElement：添加或删除的元素名为空串。
	ErrEmptyElement = errors.New("observedset: element must not be empty")
	// ErrElementNotFound：删除一个当前不存活于集合中的元素。
	ErrElementNotFound = errors.New("observedset: element is not present")
	// ErrTooManyAdds：该副本的添加记录数已达上限。
	ErrTooManyAdds = errors.New("observedset: add record limit exceeded")
	// ErrInvariantBroken：自检发现内部不变量被破坏。
	ErrInvariantBroken = errors.New("observedset: internal invariant broken")
	// ErrTagConflict：合并双方对同一标签记录了不同元素（不应发生）。
	ErrTagConflict = errors.New("observedset: conflicting element for same unique tag")
)

func formatTag(replicaID int, counter int64) string {
	return "r" + strconv.Itoa(replicaID) + "#" + strconv.FormatInt(counter, 10)
}
