// Package pcol 定义部分列更新的值类型、事件格式校验与同 Key 事件的合并；不依赖其他包。
package pcol

import "errors"
import "strconv"

// ErrBadColumn：列非法（未知列、Insert 列不全、Update 的 Set 为空或 Set/Before 列集合不同）。
var ErrBadColumn = errors.New("pcol: illegal column specification")

type Kind uint8

const KindInsert Kind = 1 // Set 恰好含全部列，Before 为空
const KindUpdate Kind = 2 // Set 非空，Before 与 Set 列集合相同

// Value 区分显式 NULL 与字符串；"" 是字符串。缺席 = map 中无此键。
type Value struct {
	Null bool
	S    string
}

// Equal：NULL==NULL，NULL!=""。
func (v Value) Equal(o Value) bool {
	if v.Null || o.Null {
		return v.Null == o.Null
	}
	return v.S == o.S
}

// Event 是上游一条变更事件。
type Event struct {
	Kind        Kind
	Key         string
	Set, Before map[string]Value
}

// Validate 对照列集合做单事件格式校验（不涉及键存在性与 Before 实际值）。
func Validate(e Event, known map[string]struct{}) error {
	for c := range e.Set {
		if _, ok := known[c]; !ok {
			return ErrBadColumn
		}
	}
	switch e.Kind {
	case KindInsert: // Set 列均已知且等长，即恰好覆盖全部列
		if len(e.Before) != 0 || len(e.Set) != len(known) {
			return ErrBadColumn
		}
	case KindUpdate: // 等长且 Before⊆Set，即两集合相同
		if len(e.Set) == 0 || len(e.Set) != len(e.Before) {
			return ErrBadColumn
		}
		for c := range e.Before {
			if _, ok := e.Set[c]; !ok {
				return ErrBadColumn
			}
		}
	default:
		return ErrBadColumn
	}
	return nil
}

// Merger 累积同 Key 的批内事件；Merge 只触及本次事件涉及的列。
// lastChecked（非导出）：最近一次 Merge 检查过的列数，每列含合并与剔除两次检查。
type Merger struct {
	Kind        Kind
	Set         map[string]Value
	firstBefore map[string]Value // 各列批内第一次更新时的 Before；永不删除
	known       map[string]struct{}
	lastChecked int
}

// NewMerger 以首条事件建立合并结果。
func NewMerger(first Event, known map[string]struct{}) (*Merger, error) {
	if err := Validate(first, known); err != nil {
		return nil, err
	}
	return &Merger{Kind: first.Kind, Set: cloneVals(first.Set), firstBefore: cloneVals(first.Before), known: known}, nil
}

// Merge 并入同 Key 的下一条（Insert/Update 之后都只接 Update）。
func (m *Merger) Merge(next Event) error {
	if err := Validate(next, m.known); err != nil {
		return err
	}
	m.lastChecked = 0
	if next.Kind != KindUpdate {
		return ErrBadColumn
	}
	for c, x := range next.Set {
		m.lastChecked += 2 // 只触及本事件列：一次合并检查 + 一次剔除检查
		if m.Kind == KindUpdate {
			b, seen := m.firstBefore[c]
			if !seen {
				b, m.firstBefore[c] = next.Before[c], next.Before[c] // 仅记录首次 Before
			}
			if x.Equal(b) {
				delete(m.Set, c) // Set==Before 剔除；firstBefore 保留供后续事件
				continue
			}
		}
		m.Set[c] = x // 同列取后到值；Insert 结果也在此吸收 Update
	}
	return nil
}

// Result 输出合并结果：Update 在此惰性剔除 Set==Before 的列，剔空则 ok=false。
func (m *Merger) Result(key string) (Event, bool) {
	if m.Kind == KindInsert {
		return Event{Kind: KindInsert, Key: key, Set: cloneVals(m.Set)}, true
	}
	set, before := map[string]Value{}, map[string]Value{}
	for c, x := range m.Set {
		if b := m.firstBefore[c]; !x.Equal(b) {
			set[c], before[c] = x, b
		}
	}
	if len(set) == 0 {
		return Event{}, false
	}
	return Event{Kind: KindUpdate, Key: key, Set: set, Before: before}, true
}

// ComplexitySelfCheck 供 demo 判定：内部读非导出计数器，只回传成败、不泄露数值。
func ComplexitySelfCheck() error {
	for _, n := range []int{100, 1000, 10000} {
		known, set, bef := map[string]struct{}{}, map[string]Value{}, map[string]Value{}
		for i := range n {
			c := "c" + strconv.Itoa(i)
			known[c], set[c], bef[c] = struct{}{}, Value{S: "n"}, Value{S: "o"}
		}
		mg, err := NewMerger(Event{Kind: KindUpdate, Set: set, Before: bef}, known)
		if err != nil {
			return err
		}
		one := Event{Kind: KindUpdate, Set: map[string]Value{"c0": {S: "z"}}, Before: map[string]Value{"c0": {S: "n"}}}
		if err := mg.Merge(one); err != nil || mg.lastChecked > 8 { // 1 列 + 与 m 无关的小常数
			return ErrBadColumn
		}
	}
	return nil
}

func cloneVals(in map[string]Value) map[string]Value {
	out := make(map[string]Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
