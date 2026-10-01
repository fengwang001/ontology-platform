package wal

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveManager 是朴素参照实现：每次 Obsolete/Purge 都遍历全部
// 内存表重算最小 first，用于与增量维护的 Manager 对拍。
type naiveManager struct {
	cur    uint64
	purged map[uint64]bool
	cfs    map[string]*naiveCF
}

type naiveCF struct {
	activeFirst uint64
	activeHas   bool
	frozen      []uint64
}

func newNaiveManager() *naiveManager {
	return &naiveManager{cur: 1, purged: make(map[uint64]bool), cfs: make(map[string]*naiveCF)}
}

func (n *naiveManager) createCF(name string) error {
	if name == "" {
		return ErrEmptyCFName
	}
	if _, ok := n.cfs[name]; ok {
		return ErrCFAlreadyExists
	}
	n.cfs[name] = &naiveCF{}
	return nil
}

func (n *naiveManager) dropCF(name string) error {
	if name == "" {
		return ErrEmptyCFName
	}
	if _, ok := n.cfs[name]; !ok {
		return ErrCFNotFound
	}
	delete(n.cfs, name)
	return nil
}

func (n *naiveManager) roll() { n.cur++ }

func (n *naiveManager) write(name string) error {
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := n.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if !cf.activeHas {
		cf.activeFirst = n.cur
		cf.activeHas = true
	}
	return nil
}

func (n *naiveManager) flushStart(name string) error {
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := n.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if !cf.activeHas {
		return ErrActiveMemtableEmpty
	}
	cf.frozen = append(cf.frozen, cf.activeFirst)
	cf.activeHas = false
	return nil
}

func (n *naiveManager) flushDone(name string) error {
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := n.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if len(cf.frozen) == 0 {
		return ErrNoFrozenMemtable
	}
	cf.frozen = cf.frozen[1:]
	return nil
}

// minFirst 遍历全部未落盘内存表，返回 first 的最小值与判定依据。
func (n *naiveManager) minFirst() (min uint64, ok bool, basis string) {
	for _, cf := range n.cfs {
		if cf.activeHas && (!ok || cf.activeFirst < min) {
			min, ok = cf.activeFirst, true
		}
		for _, f := range cf.frozen {
			if !ok || f < min {
				min = f
			}
			ok = true
		}
	}
	if ok {
		basis = fmt.Sprintf("minFirst=%d", min)
	} else {
		basis = "minFirst=none(无未落盘表)"
	}
	return min, ok, basis
}

func (n *naiveManager) obsolete() []uint64 {
	min, has, _ := n.minFirst()
	var out []uint64
	for w := uint64(1); w < n.cur; w++ {
		if n.purged[w] {
			continue
		}
		if has && w >= min {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (n *naiveManager) purge() []uint64 {
	out := n.obsolete()
	for _, w := range out {
		n.purged[w] = true
	}
	return out
}

// TestRandomDifferential 对 3000 步随机操作将 Manager 与朴素实现对拍，
// 每步在日志中打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const steps = 3000
	rng := rand.New(rand.NewSource(20261001))
	m := NewManager()
	n := newNaiveManager()
	names := []string{"cf0", "cf1", "cf2", "cf3", "", "ghost"}

	pickName := func() string { return names[rng.Intn(len(names))] }

	checkErr := func(step int, op string, gotErr, wantErr error) {
		t.Helper()
		if (gotErr == nil) != (wantErr == nil) ||
			(gotErr != nil && wantErr != nil && gotErr.Error() != wantErr.Error()) {
			t.Fatalf("step %d %s: Manager err = %v, naive err = %v", step, op, gotErr, wantErr)
		}
		out := "ok"
		if gotErr != nil {
			out = "err(" + gotErr.Error() + ")"
		}
		t.Logf("step %d 输入=%s 输出=%s 判定依据=与朴素实现拒绝原因一致", step, op, out)
	}

	for step := 0; step < steps; step++ {
		switch rng.Intn(8) {
		case 0:
			name := pickName()
			checkErr(step, "CreateCF("+name+")", m.CreateCF(name), n.createCF(name))
		case 1:
			name := pickName()
			checkErr(step, "DropCF("+name+")", m.DropCF(name), n.dropCF(name))
		case 2:
			m.Roll()
			n.roll()
			t.Logf("step %d 输入=Roll 输出=ok 判定依据=cur 变为 %d", step, n.cur)
		case 3:
			name := pickName()
			checkErr(step, "Write("+name+")", m.Write(name), n.write(name))
		case 4:
			name := pickName()
			checkErr(step, "FlushStart("+name+")", m.FlushStart(name), n.flushStart(name))
		case 5:
			name := pickName()
			checkErr(step, "FlushDone("+name+")", m.FlushDone(name), n.flushDone(name))
		case 6:
			got := m.Obsolete()
			want := n.obsolete()
			_, _, basis := n.minFirst()
			t.Logf("step %d 输入=Obsolete 输出=%v 判定依据=cur=%d %s", step, got, n.cur, basis)
			if !reflect.DeepEqual(norm(got), norm(want)) {
				t.Fatalf("step %d Obsolete: Manager = %v, naive = %v (cur=%d %s)",
					step, got, want, n.cur, basis)
			}
		case 7:
			got := m.Purge()
			want := n.purge()
			_, _, basis := n.minFirst()
			t.Logf("step %d 输入=Purge 输出=%v 判定依据=cur=%d %s", step, got, n.cur, basis)
			if !reflect.DeepEqual(norm(got), norm(want)) {
				t.Fatalf("step %d Purge: Manager = %v, naive = %v (cur=%d %s)",
					step, got, want, n.cur, basis)
			}
		}
	}
}
