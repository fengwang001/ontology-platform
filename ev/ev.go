// Package ev 定义变更事件、合法性校验与逆操作判定。不依赖其他包。
package ev

import "errors"

// Op 是变更操作的种类。
type Op int

const (
	Insert Op = iota // 插入一份
	Retract          // 撤回一份
)

// Event 表示一条变更：某个分组键上某个数值被插入或撤回。
type Event struct {
	Key string
	Val int64
	Op  Op
}

// ErrInvalid 表示事件非法（空 Key 或未知 Op）。
var ErrInvalid = errors.New("ev: invalid event")

// Validate 校验事件合法性。
func (e Event) Validate() error {
	if e.Key == "" || (e.Op != Insert && e.Op != Retract) {
		return ErrInvalid
	}
	return nil
}

// Inverse 判定两个事件是否互为逆操作：同 Key 同 Val 且 Op 相反。
func Inverse(a, b Event) bool {
	return a.Key == b.Key && a.Val == b.Val &&
		((a.Op == Insert && b.Op == Retract) || (a.Op == Retract && b.Op == Insert))
}
