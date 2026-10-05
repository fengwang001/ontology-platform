// Package vaxrule 实现疫苗系列规则与接种记录的剂次有效性判定。
package vaxrule

import (
	"errors"
	"fmt"
	"sort"
)

const (
	// Grace 为各最小日龄/最小间隔的宽限日数。
	Grace = 4
	// LiveGap 为两种活疫苗之间的最小间隔日数（同日接种不冲突）。
	LiveGap = 28
	// MaxDay 为合法日期上界（日期为 0..MaxDay 的整数日）。
	MaxDay = 1000000
	// MaxParam 为 minAge/minInt/R 各参数的上界。
	MaxParam = 10000
	// MaxDoses 为一个系列的最大剂数。
	MaxDoses = 6
)

// Series 描述一个疫苗系列的接种规则。
// MinAge 与 MinInt 为 1 基索引：MinAge[k] 为第 k 剂最小日龄，
// MinInt[k] 为第 k 剂距上一有效剂的最小间隔（k 从 2 起，MinInt[1] 不用）。
type Series struct {
	Name   string
	Live   bool
	N      int
	R      int
	MinAge []int
	MinInt []int
}

// NewSeries 校验参数并构造 Series。minAge[k-1] 为第 k 剂最小日龄，
// minInt[k-1] 为第 k 剂最小间隔（minInt[0] 不用，传 0），二者长度均须为 n。
func NewSeries(name string, live bool, n int, minAge, minInt []int, r int) (Series, error) {
	if name == "" {
		return Series{}, errors.New("vaxrule: 系列名为空")
	}
	if n < 1 || n > MaxDoses {
		return Series{}, fmt.Errorf("vaxrule: 剂数 %d 超出 1..%d", n, MaxDoses)
	}
	if len(minAge) != n || len(minInt) != n {
		return Series{}, fmt.Errorf("vaxrule: minAge/minInt 长度须为 %d", n)
	}
	for i := 0; i < n; i++ {
		if minAge[i] < 0 || minAge[i] > MaxParam || minInt[i] < 0 || minInt[i] > MaxParam {
			return Series{}, fmt.Errorf("vaxrule: 参数超出 0..%d", MaxParam)
		}
	}
	if r < 0 || r > MaxParam {
		return Series{}, fmt.Errorf("vaxrule: R 超出 0..%d", MaxParam)
	}
	s := Series{
		Name:   name,
		Live:   live,
		N:      n,
		R:      r,
		MinAge: make([]int, n+1),
		MinInt: make([]int, n+1),
	}
	copy(s.MinAge[1:], minAge)
	copy(s.MinInt[1:], minInt)
	return s, nil
}

// Status 为一条接种记录的判定结果。
type Status int

const (
	StatusValid   Status = iota // 有效
	StatusExtra                 // 多余（不算无效）
	StatusInvalid               // 无效
)

func (s Status) String() string {
	switch s {
	case StatusValid:
		return "有效"
	case StatusExtra:
		return "多余"
	default:
		return "无效"
	}
}

// Reason 为无效原因。
type Reason int

const (
	ReasonNone     Reason = iota
	ReasonAge               // 年龄
	ReasonInterval          // 间隔
	ReasonRedo              // 重打
	ReasonLive              // 活疫苗
)

func (r Reason) String() string {
	switch r {
	case ReasonAge:
		return "年龄"
	case ReasonInterval:
		return "间隔"
	case ReasonRedo:
		return "重打"
	case ReasonLive:
		return "活疫苗"
	default:
		return "无"
	}
}

// Record 为一条接种记录。
type Record struct {
	Date   int
	Series string
}

// Judgment 为一条记录的判定。
type Judgment struct {
	Rec    Record
	Status Status
	Reason Reason
}

// SeriesState 为某患者某系列的增量判定状态（LastValidDate/LastRecDate 为 -1 表示无）。
type SeriesState struct {
	Valid          int
	LastValidDate  int
	LastRecDate    int
	LastRecInvalid bool
}

// ZeroState 返回无任何记录时的初始状态。
func ZeroState() SeriesState {
	return SeriesState{LastValidDate: -1, LastRecDate: -1}
}

// JudgeOne 在既有状态 st 下判定一条日期为 d 的 S 系列记录。
// liveConflict 表示是否存在另一活疫苗系列记录日期 d′ 满足 0 < d−d′ < LiveGap。
func JudgeOne(birth int, s Series, st SeriesState, d int, liveConflict bool) (Status, Reason) {
	k := st.Valid + 1
	if k > s.N {
		return StatusExtra, ReasonNone
	}
	if d-birth < s.MinAge[k]-Grace {
		return StatusInvalid, ReasonAge
	}
	if k > 1 && d-st.LastValidDate < s.MinInt[k]-Grace {
		return StatusInvalid, ReasonInterval
	}
	if st.LastRecDate >= 0 && st.LastRecInvalid && d-st.LastRecDate < s.R {
		return StatusInvalid, ReasonRedo
	}
	if s.Live && liveConflict {
		return StatusInvalid, ReasonLive
	}
	return StatusValid, ReasonNone
}

// JudgeAll 把全部记录按（日期，系列名字节序）升序逐条判定，返回与排序后记录一一对应的判定。
func JudgeAll(birth int, series map[string]Series, recs []Record) []Judgment {
	sorted := make([]Record, len(recs))
	copy(sorted, recs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].Series < sorted[j].Series
	})
	states := make(map[string]SeriesState)
	liveRecs := make([]Record, 0, len(sorted)) // 已处理的活疫苗记录，按日期升序
	out := make([]Judgment, 0, len(sorted))
	for _, rec := range sorted {
		s := series[rec.Series]
		st, ok := states[rec.Series]
		if !ok {
			st = ZeroState()
		}
		conflict := false
		if s.Live {
			lo := sort.Search(len(liveRecs), func(i int) bool {
				return liveRecs[i].Date >= rec.Date-LiveGap+1
			})
			for i := lo; i < len(liveRecs) && liveRecs[i].Date <= rec.Date-1; i++ {
				if liveRecs[i].Series != rec.Series {
					conflict = true
					break
				}
			}
		}
		status, reason := JudgeOne(birth, s, st, rec.Date, conflict)
		st.LastRecDate = rec.Date
		st.LastRecInvalid = status == StatusInvalid
		if status == StatusValid {
			st.Valid++
			st.LastValidDate = rec.Date
		}
		states[rec.Series] = st
		if s.Live {
			liveRecs = append(liveRecs, rec)
		}
		out = append(out, Judgment{Rec: rec, Status: status, Reason: reason})
	}
	return out
}

// EarliestBase 返回下一剂序号与不计活疫苗冲突时的最早可接种日下界。
// ok 为 false 表示系列已完成。
func EarliestBase(birth int, s Series, st SeriesState, now int) (dose, base int, ok bool) {
	if st.Valid >= s.N {
		return 0, 0, false
	}
	k := st.Valid + 1
	lo := now
	if t := birth + s.MinAge[k] - Grace; t > lo {
		lo = t
	}
	if k > 1 {
		if t := st.LastValidDate + s.MinInt[k] - Grace; t > lo {
			lo = t
		}
	}
	if st.LastRecDate >= 0 && st.LastRecInvalid {
		if t := st.LastRecDate + s.R; t > lo {
			lo = t
		}
	}
	return k, lo, true
}

// AdjustLive 在活疫苗 28 日窗口约束下推进候选日：maxConflict(d) 返回窗口
// [d−LiveGap+1, d−1] 内另一活疫苗系列记录的最大日期，无则返回 -1。
func AdjustLive(s Series, lo int, maxConflict func(d int) int) int {
	if !s.Live {
		return lo
	}
	for {
		m := maxConflict(lo)
		if m < 0 {
			return lo
		}
		lo = m + LiveGap
	}
}
