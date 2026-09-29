// Package ontology 提供有序键值快照的归并差分与版本化管理。
package ontology

import (
	"context"
	"strconv"
)

// Op 表示一条变更的种类。
type Op string

const (
	OpInsert Op = "insert"
	OpDelete Op = "delete"
	OpUpdate Op = "update"
)

// Entry 是快照中的一个键值条目。
type Entry struct {
	Key   string
	Value string
}

// Change 是一条按键排序的变更日志记录。
type Change struct {
	Op       Op
	Key      string
	OldValue string
	NewValue string
}

// ErrorKind 标识一次整体拒绝的错误类别。
type ErrorKind string

const (
	KindInvalidConfig  ErrorKind = "invalid config"
	KindEmptyKey       ErrorKind = "empty key"
	KindDuplicateKey   ErrorKind = "duplicate key"
	KindUnsorted       ErrorKind = "unsorted"
	KindTooManyChanges ErrorKind = "too many changes"
)

// Error 表示一次被整体拒绝的差分请求。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Kind) + ": " + e.Msg
}

// Config 是 Manager 的配置。
type Config struct {
	MaxChanges int
}

// Stats 是自管理器创建以来的累计统计。
type Stats struct {
	Commits      int64
	Inserts      int64
	Deletes      int64
	Updates      int64
	ChangesTotal int64
}

// Logger 记录差分过程中的每一步判定。
type Logger interface {
	Logf(ctx context.Context, format string, args ...any)
}

// Validate 校验快照：键非空且按键严格升序。
//
// 从前往后扫描，在第一处违规处停止并给出判定依据：
//   - 当前位置键为空：ErrEmptyKey；
//   - 当前位置键与前一键相等：ErrDuplicateKey（重复键优先于未排序）；
//   - 当前位置键小于前一键：ErrUnsorted。
func Validate(snapshot []Entry) error {
	for i := range snapshot {
		if snapshot[i].Key == "" {
			return reject(KindEmptyKey, "entry at index "+strconv.Itoa(i)+" has empty key")
		}
		if i > 0 {
			prev := snapshot[i-1].Key
			cur := snapshot[i].Key
			if cur == prev {
				return reject(KindDuplicateKey,
					"key "+cur+" duplicated at indices "+strconv.Itoa(i-1)+" and "+strconv.Itoa(i))
			}
			if cur < prev {
				return reject(KindUnsorted,
					"key "+cur+" at index "+strconv.Itoa(i)+" is not greater than previous key "+prev)
			}
		}
	}
	return nil
}
