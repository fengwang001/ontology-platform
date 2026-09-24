// Package api 对外入口：New/Apply/Row/SelfCheck；依赖方向 api → cbatch → pcol。
package api

import (
	"errors"
	"maps"
	"math/rand"
	"reflect"
	"strconv"

	"ontology/cbatch"
	"ontology/pcol"
)

type Kind = pcol.Kind
type Value = pcol.Value
type Event = pcol.Event

const KindInsert, KindUpdate = pcol.KindInsert, pcol.KindUpdate

var ErrBadColumn = pcol.ErrBadColumn
var ErrKeyNotFound = cbatch.ErrKeyNotFound
var ErrKeyExists = cbatch.ErrKeyExists
var ErrBeforeMismatch = cbatch.ErrBeforeMismatch
var errInvariant = errors.New("api: self-check invariant violated")

type Engine struct{ t *cbatch.Table }

func New(cols []string) (*Engine, error) {
	t, err := cbatch.NewTable(cols)
	if err != nil {
		return nil, err
	}
	return &Engine{t: t}, nil
}
func (e *Engine) Apply(batch []Event) ([]Event, error)    { return e.t.Apply(batch) }
func (e *Engine) Row(key string) (map[string]Value, bool) { return e.t.Row(key) }

// SelfCheck 用内置随机批核验四条不变量：朴素一致、Before 镜像、至多一条且保序、失败不留痕。
func (e *Engine) SelfCheck() error {
	rng := rand.New(rand.NewSource(11))
	keys := []string{"k0", "k1", "k2", "k3", "k4", "k5"}
	for range 300 {
		pre := map[string]map[string]Value{} // 六个 Key 的批前快照
		for _, k := range keys {
			if r, ok := e.Row(k); ok {
				pre[k] = r
			}
		}
		shadow, batch, first, bad := map[string]map[string]Value{}, []Event{}, []string{}, false
		for j := rng.Intn(8) + 1; j > 0; j-- {
			k := keys[rng.Intn(len(keys))]
			cur, seen := shadow[k]
			if !seen {
				first = append(first, k)
				cur = maps.Clone(pre[k]) // 批前不存在时为 nil
			}
			if cur == nil {
				s := map[string]Value{"a": rv(rng), "b": rv(rng), "c": rv(rng)}
				batch = append(batch, Event{Kind: KindInsert, Key: k, Set: s})
				shadow[k] = maps.Clone(s)
				continue
			}
			s, b0 := map[string]Value{}, map[string]Value{}
			for _, c := range []string{"a", "b", "c"} {
				if rng.Intn(2) == 0 {
					old, x := cur[c], rv(rng)
					s[c], cur[c] = x, x
					if bad || rng.Intn(3) != 0 {
						b0[c] = old
					} else {
						b0[c], bad = Value{S: "!!!"}, true // 注入不可能的 Before
					}
				}
			}
			if len(s) == 0 {
				x := rv(rng)
				s["a"], b0["a"], cur["a"] = x, cur["a"], x
			}
			batch = append(batch, Event{Kind: KindUpdate, Key: k, Set: s, Before: b0})
			shadow[k] = cur
		}
		out, err := e.Apply(batch)
		if bad { // 不变量 4：整批被拒且六行快照不变
			if !errors.Is(err, ErrBeforeMismatch) {
				return errInvariant
			}
			for _, k := range keys {
				r, ok := e.Row(k)
				if ok != (pre[k] != nil) || ok && !rowsEq(r, pre[k]) {
					return errInvariant
				}
			}
			continue
		}
		if err != nil {
			return err
		}
		kept, pi := map[string]bool{}, 0
		for _, o := range out { // 不变量 3：每 Key 至多一条，顺序等于首次出现（剔空者跳过）
			if kept[o.Key] {
				return errInvariant
			}
			kept[o.Key] = true
			for pi < len(first) && !kept[first[pi]] {
				pi++
			}
			if pi >= len(first) || first[pi] != o.Key {
				return errInvariant
			}
			pi++
		}
		merged := map[string]map[string]Value{}
		for k, r := range pre {
			merged[k] = maps.Clone(r) // 被剔空不输出的 Key 回退为批前行
		}
		for _, o := range out {
			row := maps.Clone(pre[o.Key])
			if o.Kind == KindInsert {
				row = maps.Clone(o.Set)
			}
			for c, x := range o.Set {
				if o.Kind == KindUpdate { // 不变量 2：Before 镜像批前值，且无 Set==Before
					b := pre[o.Key][c]
					if !b.Equal(o.Before[c]) || x.Equal(b) {
						return errInvariant
					}
				}
				row[c] = x // 缺席列不在 o.Set 中，自然保持批前值
			}
			merged[o.Key] = row
		}
		for k, w := range shadow { // 不变量 1：重建结果与真实表都等于朴素参照
			if r, _ := e.Row(k); !rowsEq(r, w) {
				return errInvariant
			}
			if !rowsEq(merged[k], w) {
				return errInvariant
			}
		}
	}
	return nil
}
func rv(r *rand.Rand) Value {
	return []Value{{Null: true}, {S: ""}, {S: strconv.Itoa(r.Intn(5))}}[r.Intn(3)]
}
func rowsEq(a, b map[string]Value) bool { return reflect.DeepEqual(a, b) }
