// Package api is the public face of the incremental Top-N materialised view.
package api

import (
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"ontology/rank"
	"ontology/topn"
)

type (
	Change = topn.Change
	Op     = topn.Op
)

type Row struct {
	Key   string
	Score int64
}

var (
	ErrInvalidN     = errors.New("api: N must be > 0 and <= maxRows")
	ErrDuplicateKey = topn.ErrDuplicateKey
	ErrMissingRow   = topn.ErrMissingRow
	ErrTooManyRows  = topn.ErrTooManyRows
	errSelf         = errors.New("api: selfcheck failed")
)

type MView struct{ v *topn.View }

// New constructs an MView with boundary N and live-row cap maxRows.
func New(n, maxRows int) (*MView, error) {
	if n <= 0 || maxRows < n {
		return nil, ErrInvalidN
	}
	v, err := topn.New(n, maxRows)
	return &MView{v: v}, err
}

func (m *MView) Apply(ops []Op) ([]Change, error) { return m.v.Apply(ops) }
func (m *MView) Live() int                        { return m.v.Live() }
func (m *MView) View() []Row                      { return toRows(m.v.Top()) }
func toRows(t []rank.Row) []Row {
	r := make([]Row, len(t))
	for i := range t {
		r[i] = Row{Key: t[i].Key, Score: t[i].Score}
	}
	return r
}

// model is the batch-recompute oracle.
func model(live map[string]int64, n int) []Row {
	a := make([]rank.Row, 0, len(live))
	for k, s := range live {
		a = append(a, rank.Row{Key: k, Score: s})
	}
	sort.Slice(a, func(i, j int) bool { return rank.Before(a[i], a[j]) })
	return toRows(a[:min(n, len(a))])
}

// dec parses a compact token "+a50"/"-b70" into key, score, entering.
func dec(s string) (string, int64, bool) {
	v, _ := strconv.ParseInt(s[2:], 10, 64)
	return s[1:2], v, s[0] == '+'
}
func opOf(s string) Op      { k, v, a := dec(s); return Op{Key: k, Score: v, Add: a} }
func chgOf(s string) Change { k, v, a := dec(s); return Change{Key: k, Score: v, Entering: a} }
func fields(s string) []string {
	return append([]string{}, strings.Fields(s)...) // non-nil even when s==""
}

// replay asserts prefix self-consistency and the final held count.
func replay(log []Change, want int) error {
	h := map[string]int64{}
	for _, c := range log {
		s, ok := h[c.Key]
		if c.Entering == ok || (!c.Entering && s != c.Score) {
			return errSelf
		}
		if c.Entering {
			h[c.Key] = c.Score
		} else {
			delete(h, c.Key)
		}
	}
	if len(h) != want {
		return errSelf
	}
	return nil
}

func check(mv *MView, live map[string]int64, all []Change, op Op, es []string, n int) ([]Change, error) {
	em, err := mv.Apply([]Op{op})
	if err != nil || (es != nil && len(em) != len(es)) {
		return all, errSelf
	}
	for j := range es {
		if em[j] != chgOf(es[j]) {
			return all, errSelf
		}
	}
	if op.Add {
		live[op.Key] = op.Score
	} else {
		delete(live, op.Key)
	}
	all = append(all, em...)
	if !reflect.DeepEqual(mv.View(), model(live, n)) || replay(all, min(n, len(live))) != nil {
		return all, errSelf
	}
	return all, nil
}

// SelfCheck runs the built-in ten-step sequence (invariants 1, 2, 3) and
// the four rejections with an atomic batch (invariant 4).
func (m *MView) SelfCheck() error {
	seq := strings.Fields("+a50 +b70 +c50 +d60 +e50 -b70 -e50 +f55 -d60 +g45")
	exp := strings.Split("+a50|+b70|+c50|-c50 +d60||-b70 +c50||-c50 +f55|-d60 +c50|", "|")
	mv, _ := New(3, 16)
	live, all := map[string]int64{}, []Change{}
	for i, s := range seq {
		var e error
		if all, e = check(mv, live, all, opOf(s), fields(exp[i]), 3); e != nil {
			return e
		}
	}
	for _, c := range [][2]int{{0, 5}, {5, 3}} {
		if _, e := New(c[0], c[1]); !errors.Is(e, ErrInvalidN) {
			return errSelf
		}
	}
	fv, _ := New(2, 4)
	fv.Apply([]Op{{Key: "a", Score: 1, Add: true}, {Key: "b", Score: 1, Add: true}, {Key: "c", Score: 1, Add: true}, {Key: "d", Score: 1, Add: true}})
	badOps := []Op{{Key: "a", Score: 1, Add: true}, {Key: "q"}, {Key: "a", Score: 9}, {Key: "z", Score: 1, Add: true}}
	badErrs := []error{ErrDuplicateKey, ErrMissingRow, ErrMissingRow, ErrTooManyRows}
	for i := range badOps {
		if _, e := fv.Apply([]Op{badOps[i]}); !errors.Is(e, badErrs[i]) {
			return errSelf
		}
	}
	l, vw := fv.Live(), fv.View()
	if _, e := fv.Apply([]Op{{Key: "b", Score: 1}, {Key: "z", Score: 2, Add: true}, {Key: "q", Score: 2}}); !errors.Is(e, ErrMissingRow) || fv.Live() != l || !reflect.DeepEqual(fv.View(), vw) {
		return errSelf
	}
	return nil
}
