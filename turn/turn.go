// Package turn 负责单个回合的结算与缺失填充。
package turn

// Source 标记一条记录的输入来源。
type Source byte

const (
	Real   Source = iota // 实交：玩家本人提交
	Repeat               // 重复：填充该玩家最近一次实交
	Empty                // 空填：无最近实交或超出重复上限
)

// String 返回来源的可读名称。
func (s Source) String() string {
	switch s {
	case Real:
		return "实交"
	case Repeat:
		return "重复"
	case Empty:
		return "空填"
	}
	return "未知"
}

// Entry 是某玩家在一个回合的记录。
type Entry struct {
	Input []byte // 本回合采用的输入；空填时为 nil
	Src   Source
}

// Log 是一个已结算回合的完整记录，Entries 按玩家编号 0..N-1 排列。
type Log struct {
	Turn    int
	Ts      int64
	Entries []Entry
}

// Player 是结算所需的每玩家状态。
type Player struct {
	Active  bool   // 活跃标记；非活跃者仍产生记录，但不参与到齐判定
	M       int    // 连续缺失数
	Last    []byte // 最近一次实交的输入
	HasLast bool   // 是否有过实交
}

// Settle 结算回合 k：为每个玩家（含非活跃者）产生一条记录，并就地更新状态。
// get 返回玩家已提交的输入；repeat 为重复填充上限 R，threshold 为掉线阈值 Kd；
// onDeactivate 在玩家被置为非活跃时回调（可为 nil）。
func Settle(k int, ts int64, ps []Player, get func(p int) ([]byte, bool), repeat, threshold int, onDeactivate func(p int)) Log {
	lg := Log{Turn: k, Ts: ts, Entries: make([]Entry, len(ps))}
	for p := range ps {
		pl := &ps[p]
		if cmd, ok := get(p); ok {
			pl.M = 0
			pl.Last = append([]byte(nil), cmd...)
			pl.HasLast = true
			lg.Entries[p] = Entry{Input: cmd, Src: Real}
			continue
		}
		pl.M++
		if pl.M <= repeat && pl.HasLast {
			lg.Entries[p] = Entry{Input: pl.Last, Src: Repeat}
		} else {
			lg.Entries[p] = Entry{Src: Empty}
		}
		if pl.M >= threshold && pl.Active {
			pl.Active = false
			if onDeactivate != nil {
				onDeactivate(p)
			}
		}
	}
	return lg
}
