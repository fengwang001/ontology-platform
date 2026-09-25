// Package api 是对外门面：组合 backfill 引擎，暴露题目要求的全部操作。
// 依赖方向 api -> backfill -> dedup，单向无反向。
package api

import (
	"ontology/backfill"
)

// Event 对外事件类型，与 backfill.Event 同一类型（别名，不反向依赖）。
type Event = backfill.Event

// 可判定的哨兵错误，四类互不相同，直接再导出 backfill 的哨兵。
var (
	ErrNegativeW      = backfill.ErrNegativeW
	ErrSeqOutOfRange  = backfill.ErrSeqOutOfRange
	ErrBackfillClosed = backfill.ErrBackfillClosed
	ErrEmptyKey       = backfill.ErrEmptyKey
)

// API 是增量回填去重计数视图的对外句柄。
type API struct {
	eng *backfill.Engine
}

// New 以切换水位 W 创建实例；W 为负返回 ErrNegativeW。
func New(w int64) (*API, error) {
	eng, err := backfill.New(w)
	if err != nil {
		return nil, err
	}
	return &API{eng: eng}, nil
}

// Backfill 应用一批历史事件（全部 Seq < W）；整批原子，失败零改变。
func (a *API) Backfill(evs []Event) error { return a.eng.Backfill(evs) }

// Online 应用一批在线事件（Seq 任意）；整批原子，失败零改变。
func (a *API) Online(evs []Event) error { return a.eng.Online(evs) }

// CompleteBackfill 标记回填完成；不改变任何已正确计数。
func (a *API) CompleteBackfill() error { return a.eng.Complete() }

// View 返回每个 Key 的去重计数快照。
func (a *API) View() map[string]int64 { return a.eng.View() }

// Seen 返回已应用的不同 Seq 总数。
func (a *API) Seen() int { return a.eng.Seen() }

// SelfCheck 核验全部不变量，被破坏时返回非 nil 错误。
func (a *API) SelfCheck() error { return a.eng.SelfCheck() }
