// Package rec 定义变更日志记录及其判定规则，不依赖其他包。
package rec

import "errors"

// Rec 是一条按 Key 分组的变更日志记录。Del=true 表示墓碑（删除）。
type Rec struct {
	Key   string
	Value int
	TS    int64
	Del   bool
}

// 可判定的哨兵错误。
var (
	ErrEmptyKey   = errors.New("rec: empty key")
	ErrNegativeTS = errors.New("rec: negative ts")
)

// Validate 校验记录合法性：Key 非空、TS 非负。
func (r Rec) Validate() error {
	if r.Key == "" {
		return ErrEmptyKey
	}
	if r.TS < 0 {
		return ErrNegativeTS
	}
	return nil
}

// Drop 判定一条存活记录是否应被保留期丢弃：
// 仅当它是墓碑且 TS+R <= hi 时丢弃；普通记录一律保留。
func Drop(r Rec, hi, R int64) bool {
	return r.Del && r.TS+R <= hi
}
