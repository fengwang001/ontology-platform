// Package lww 持有两级 LWW-Map 的核心状态：outer -> inner -> (ts, rep, v)。
// 规则：键级 LWW（ts 大者胜，平局 rep 字典序大者胜）；外层墓碑只抬阈值
// 不清空 inner；可见当且仅当 ts > 墓碑 ts；Merge 增量传播墓碑与 inner。
package lww

import "errors"

// 三类可判定、互不相同的哨兵错误（在任何状态修改之前校验）。
var (
	ErrEmptyOuter    = errors.New("lww: outer key must not be empty")
	ErrEmptyInner    = errors.New("lww: inner key must not be empty")
	ErrNonPositiveTS = errors.New("lww: timestamp must be positive")
)

// Entry 是带 LWW 标签的内层值。
type Entry struct {
	TS  int64
	Rep string
	V   int64
}

// Map 是两级 LWW-Map；零值不可用，请用 New。
type Map struct {
	tomb map[string]int64 // 外层墓碑 ts（缺省为 0）
	data map[string]map[string]Entry
}

// New 创建空状态。
func New() *Map {
	return &Map{tomb: map[string]int64{}, data: map[string]map[string]Entry{}}
}

// Put 写入 inner[i]。外层有墓碑且 ts <= 墓碑 ts 时为迟到写，整体忽略
// （不复活、不写 inner）；否则按键级 LWW 与既有条目取大。
func (m *Map) Put(o, i string, v, ts int64, rep string) error {
	if o == "" {
		return ErrEmptyOuter
	}
	if i == "" {
		return ErrEmptyInner
	}
	if ts <= 0 {
		return ErrNonPositiveTS
	}
	if ts <= m.tomb[o] {
		return nil // 迟到写：被墓碑覆盖，整体忽略
	}
	im := m.data[o]
	if im == nil {
		im = map[string]Entry{}
		m.data[o] = im
	}
	e := Entry{TS: ts, Rep: rep, V: v}
	if cur, ok := im[i]; !ok || winner(e, cur) == e {
		im[i] = e
	}
	return nil
}

// DelOuter 把外层墓碑抬到 max(墓碑, ts)，不物理清空 inner。
func (m *Map) DelOuter(o string, ts int64) error {
	if o == "" {
		return ErrEmptyOuter
	}
	if ts <= 0 {
		return ErrNonPositiveTS
	}
	if ts > m.tomb[o] {
		m.tomb[o] = ts
	}
	return nil
}

// Merge 增量合并另一副本：每个外层键墓碑取两侧 max，inner 取并集并逐键
// 按键级 LWW 取大（顺序无关，双向合并收敛到同一状态）。
func (m *Map) Merge(other *Map) {
	if other == nil {
		return
	}
	for o, t := range other.tomb { // 墓碑传播
		if t > m.tomb[o] {
			m.tomb[o] = t
		}
	}
	for o, oim := range other.data { // inner 并集 + 键级 LWW
		im := m.data[o]
		if im == nil {
			im = map[string]Entry{}
			m.data[o] = im
		}
		for i, e := range oim {
			if cur, ok := im[i]; !ok || winner(e, cur) == e {
				im[i] = e
			}
		}
	}
}

// Tomb 返回外层键当前墓碑 ts（主要供测试/观测使用）。
func (m *Map) Tomb(o string) int64 { return m.tomb[o] }

// View 返回每个外层键下所有可见（ts > 墓碑 ts）内层条目；没有可见条目的
// 外层键不出现。返回的是深拷贝，调用方修改不影响内部状态。
func (m *Map) View() map[string]map[string]int64 {
	view := map[string]map[string]int64{}
	for o, im := range m.data {
		t := m.tomb[o]
		vis := map[string]int64{}
		for i, e := range im {
			if e.TS > t {
				vis[i] = e.V
			}
		}
		if len(vis) > 0 {
			view[o] = vis
		}
	}
	return view
}

// winner 按键级 LWW 取胜者：ts 大者胜，平局 rep 字典序大者胜。
func winner(a, b Entry) Entry {
	if b.TS > a.TS || (b.TS == a.TS && b.Rep > a.Rep) {
		return b
	}
	return a
}
