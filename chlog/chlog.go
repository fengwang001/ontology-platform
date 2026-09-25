// Package chlog 维护变更日志列表与下游视图：按序应用 +/-，
// 维护 key→sum，并在追加时做撤回一致性校验。依赖 agg。
package chlog

import (
	"errors"

	"ontology/agg"
)

// ErrInconsistent 变更日志不满足撤回一致性：- 撤回的不是当前值，
// 或 + 时该 key 仍持有未撤回的值。
var ErrInconsistent = errors.New("chlog: inconsistent change sequence")

// Change 一条变更日志。Plus 为 true 表示 +(key,Sum)，否则 -(key,Sum)。
type Change struct {
	Key  string
	Plus bool
	Sum  int64
}

// Log 变更日志与按序重放得到的下游视图。
type Log struct {
	changes []Change
	view    map[string]int64
}

// New 创建空日志。
func New() *Log { return &Log{view: map[string]int64{}} }

// Append 校验后追加一条变更：
//   - - 必须撤回恰好等于该 key 当前值的已存在值；
//   - + 时该 key 不得仍持有值（必须先撤回，或这是首条）。
//
// 校验失败返回 ErrInconsistent 且不留痕。
func (l *Log) Append(c Change) error {
	cur, ok := l.view[c.Key]
	if c.Plus {
		if ok {
			return ErrInconsistent // 仍持有旧值，不得再 +
		}
	} else {
		if !ok || cur != c.Sum {
			return ErrInconsistent // 撤回不存在或不等的值
		}
	}
	l.changes = append(l.changes, c)
	if c.Plus {
		l.view[c.Key] = c.Sum
	} else {
		delete(l.view, c.Key)
	}
	return nil
}

// Emit 按规则把一次 agg.Apply 的结果转成变更日志条目并追加，返回新条目：
// 该 key 首次有数据只产出 +；sum 变化产出 -old 与 +new；sum 不变无产出。
func (l *Log) Emit(key string, first bool, r agg.Result) ([]Change, error) {
	var emit []Change
	if first {
		emit = append(emit, Change{Key: key, Plus: true, Sum: r.NewSum})
	} else if r.OldSum != r.NewSum {
		emit = append(emit,
			Change{Key: key, Plus: false, Sum: r.OldSum},
			Change{Key: key, Plus: true, Sum: r.NewSum})
	}
	for _, c := range emit {
		if err := l.Append(c); err != nil {
			return nil, err
		}
	}
	return emit, nil
}

// Changes 返回变更日志的副本。
func (l *Log) Changes() []Change {
	out := make([]Change, len(l.changes))
	copy(out, l.changes)
	return out
}

// View 返回下游视图的副本（当前持有值的 key→sum）。
func (l *Log) View() map[string]int64 {
	out := make(map[string]int64, len(l.view))
	for k, v := range l.view {
		out[k] = v
	}
	return out
}
