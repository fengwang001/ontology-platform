// Package api 是对外接口：喂入变更、确定性重放、按键查历史、自检。依赖 lww。
package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"

	"ontology/lww"
	"ontology/seq"
)

// Change 是输入变更；Feed 时 Sn 字段由系统分配，调用方填入的值被忽略。
type Change = seq.Change

// Record 是压缩后的输出项：(Key, Val) 对。
type Record = lww.Record

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrEmptyKey     = errors.New("api: empty key")
	ErrNegativeVer  = errors.New("api: negative ver")
	ErrHistoryLimit = errors.New("api: history limit exceeded")
)

// API 是变更流重放系统，并发安全。
type API struct {
	mu  sync.RWMutex
	eng *lww.Engine
}

// New 返回空系统，maxHistory 为单键历史变更数上限。
func New(maxHistory int) *API {
	return &API{eng: lww.New(maxHistory)}
}

// Feed 喂入一批变更。任一条非法（空 Key、负 Ver、历史超限）则整批不生效：
// 先整体校验再整体应用，历史、winner、序号全部不变。
func (a *API) Feed(chgs []Change) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	add := make(map[string]int)
	for _, c := range chgs {
		if c.Key == "" {
			return ErrEmptyKey
		}
		if c.Ver < 0 {
			return ErrNegativeVer
		}
		add[c.Key]++
	}
	for k, n := range add {
		if !a.eng.Fits(k, n) {
			return ErrHistoryLimit
		}
	}
	for _, c := range chgs {
		a.eng.Apply(c.Key, c.Ver, c.Val)
	}
	return nil
}

// Replay 返回压缩后的全部 (Key, Val)，按 Key 字典序升序。
// 纯函数：不改变任何状态，同一输入多次调用输出逐字节相同。
func (a *API) Replay() []Record {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.Replay()
}

// History 返回该键的全部变更，按 sn 升序。
func (a *API) History(key string) []Change {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.History(key)
}

// brute 是朴素参照：逐键取 Ver 最大者（并列取 sn 最大者），按 Key 升序输出。
func brute(keys []string, hist func(string) []Change) []Record {
	var out []Record
	for _, k := range keys {
		hs := hist(k)
		if len(hs) == 0 {
			continue
		}
		w := hs[0]
		for _, c := range hs[1:] {
			if c.Ver > w.Ver || (c.Ver == w.Ver && c.Sn > w.Sn) {
				w = c
			}
		}
		out = append(out, Record{Key: k, Val: w.Val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SelfCheck 在全新实例上用内置变更序列核验四条不变量，不触碰接收者状态，
// 因此可与其他只读调用并发。全部成立返回 true。
func (a *API) SelfCheck() bool {
	fresh := New(8)
	batch := []Change{
		{Key: "a", Ver: 10, Val: 100}, {Key: "b", Ver: 5, Val: 50},
		{Key: "a", Ver: 10, Val: 200}, {Key: "c", Ver: 7, Val: 70},
		{Key: "a", Ver: 5, Val: 50}, {Key: "b", Ver: 12, Val: 90},
		{Key: "c", Ver: 7, Val: 77},
	}
	if err := fresh.Feed(batch); err != nil {
		return false
	}
	keys := []string{"a", "b", "c"}
	// 不变量 1：与朴素参照一致。
	if !reflect.DeepEqual(fresh.Replay(), brute(keys, fresh.History)) {
		return false
	}
	// 不变量 2：幂等确定，连调两次逐字段相同，且历史顺序恒为 sn 升序。
	if !reflect.DeepEqual(fresh.Replay(), fresh.Replay()) {
		return false
	}
	for _, k := range keys {
		hs := fresh.History(k)
		for i := 1; i < len(hs); i++ {
			if hs[i].Sn <= hs[i-1].Sn {
				return false
			}
		}
	}
	// 不变量 3：winner 的 Ver 为最大、并列时 sn 最大（由不变量 1 的逐键比对覆盖，
	// 这里再直接核对 a 的 winner Val=200）。
	if fresh.Replay()[0] != (Record{Key: "a", Val: 200}) {
		return false
	}
	// 不变量 4：三类被拒操作均不留痕。
	before := fresh.Replay()
	if err := fresh.Feed([]Change{{Key: "", Ver: 1, Val: 1}}); !errors.Is(err, ErrEmptyKey) {
		return false
	}
	if err := fresh.Feed([]Change{{Key: "a", Ver: -1, Val: 1}}); !errors.Is(err, ErrNegativeVer) {
		return false
	}
	over := make([]Change, 9) // a 已有 3 条历史，上限 8，再喂 9 条必超限
	for i := range over {
		over[i] = Change{Key: "a", Ver: int64(i), Val: int64(i)}
	}
	if err := fresh.Feed(over); !errors.Is(err, ErrHistoryLimit) {
		return false
	}
	return reflect.DeepEqual(fresh.Replay(), before)
}
