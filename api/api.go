// Package api 对外门面：建库、演进、解码、自检。
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/decode"
	"ontology/schema"
)

type Column = schema.Column
type Op = schema.Op
type Event = decode.Event

var Add = schema.Add
var Drop = schema.Drop
var Rename = schema.Rename

var (
	ErrInvalidOp       = schema.ErrInvalidOp
	ErrTooManyVersions = schema.ErrTooManyVersions
	ErrVersionNotFound = decode.ErrVersionNotFound
	ErrValueCount      = decode.ErrValueCount
	errSelfCheck       = errors.New("api: self check failed")
)

// API 并发安全：解码读快照，演进加写锁。
type API struct {
	h   *schema.History
	dec *decode.Decoder
}

func New(cols []Column, maxVersions int) (*API, error) {
	h, err := schema.New(cols, maxVersions)
	if err != nil {
		return nil, err
	}
	return &API{h: h, dec: decode.New(h)}, nil
}

func (a *API) Evolve(ops []Op) (int, error) { return a.h.Evolve(ops) }
func (a *API) Decode(ev Event) ([]string, error) {
	return a.dec.Decode(ev)
}
func (a *API) DecodeBatch(evs []Event) ([][]string, error) {
	return a.dec.DecodeBatch(evs)
}

// ReadSchema 返回当前读 schema 的列副本。
func (a *API) ReadSchema() []Column { return slices.Clone(a.h.Latest().Cols) }

// replayRow 朴素参照：从事件版本起逐版本按名字重放 ops 直到最新版本。
func replayRow(init []Column, opsPerVer [][]Op, ev Event) []string {
	type col struct{ name, def string }
	cols := make([]col, len(init))
	for i, c := range init {
		cols[i] = col{c.Name, c.Default}
	}
	row := map[string]string{}
	apply := func(ops []Op) {
		for _, op := range ops {
			switch op.Kind {
			case schema.OpAdd:
				cols = append(cols, col{op.Name, op.Default})
				row[op.Name] = op.Default
			case schema.OpDrop:
				if i := slices.IndexFunc(cols, func(c col) bool { return c.name == op.Name }); i >= 0 {
					cols = slices.Delete(cols, i, i+1)
				}
				delete(row, op.Name)
			case schema.OpRename:
				if i := slices.IndexFunc(cols, func(c col) bool { return c.name == op.Name }); i >= 0 {
					cols[i].name = op.NewName
				}
				row[op.NewName] = row[op.Name]
				delete(row, op.Name)
			}
		}
	}
	for v := 2; v <= ev.Version; v++ {
		apply(opsPerVer[v-2])
	}
	row = map[string]string{}
	for i, c := range cols {
		row[c.name] = ev.Values[i]
	}
	for v := ev.Version + 1; v <= len(opsPerVer)+1; v++ {
		apply(opsPerVer[v-2])
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = row[c.name]
	}
	return out
}

// SelfCheck 用内置演进与事件序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	init := []Column{{Name: "id"}, {Name: "name"}, {Name: "age"}}
	ops := [][]Op{
		{Rename("name", "full_name"), Add("email", "none")},
		{Drop("age"), Add("age", "unknown")},
	}
	a, err := New(init, 10)
	if err != nil {
		return err
	}
	for _, o := range ops {
		if _, err = a.Evolve(o); err != nil {
			return err
		}
	}
	if id := a.ReadSchema()[3].ID; id != 5 {
		return fmt.Errorf("%w: id reused, age id=%d", errSelfCheck, id) // 不变量 2
	}
	evs := []Event{
		{Version: 1, Values: []string{"1", "Ann", "30"}},
		{Version: 2, Values: []string{"2", "Bob", "41", "b@x"}},
		{Version: 3, Values: []string{"3", "Cy", "c@x", "25"}},
		{Version: 2, Values: []string{"4", "Di", "19", ""}},
	}
	for _, ev := range evs {
		got, err := a.Decode(ev)
		if err != nil {
			return err
		}
		if !slices.Equal(got, replayRow(init, ops, ev)) {
			return fmt.Errorf("%w: replay mismatch v%d", errSelfCheck, ev.Version) // 不变量 1
		}
		if ev.Version == 3 && !slices.Equal(got, ev.Values) {
			return fmt.Errorf("%w: current version altered", errSelfCheck) // 不变量 3
		}
	}
	// 不变量 4：被拒操作不改变状态（含 ID 计数器）。
	before := a.ReadSchema()
	_, err1 := a.Evolve([]Op{Add("x", ""), Drop("nope")})
	_, err2 := a.Decode(Event{Version: 9})
	n, err3 := a.Evolve([]Op{Add("z", "z")})
	if !errors.Is(err1, ErrInvalidOp) || !errors.Is(err2, ErrVersionNotFound) ||
		err3 != nil || n != 4 || a.ReadSchema()[4].ID != 6 || !slices.Equal(before, a.ReadSchema()[:4]) {
		return fmt.Errorf("%w: rejection left trace", errSelfCheck)
	}
	return nil
}
