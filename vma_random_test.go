package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveVMA struct {
	VMA
}

type naiveManager struct {
	low, high, guard, maxStack int64
	maxV                       int
	pages                      []naivePage
}

type naivePage struct {
	present   bool
	perm      int
	growsDown bool
	anon      bool
	file      int64
	off       int64
	stack     bool
}

func newNaive(low, high int64, maxV int, guard, maxStack int64) *naiveManager {
	return &naiveManager{low: low, high: high, guard: guard, maxStack: maxStack, maxV: maxV,
		pages: make([]naivePage, high-low)}
}

func (n *naiveManager) at(addr int64) *naivePage { return &n.pages[addr-n.low] }

func (n *naiveManager) vmAs() []VMA {
	var out []VMA
	for addr := n.low; addr < n.high; {
		p := n.at(addr)
		if !p.present {
			addr++
			continue
		}
		start := addr
		startPage := *p
		for addr < n.high && n.samePage(*n.at(addr), *p) {
			addr++
		}
		vma := VMA{Start: start, End: addr, Perm: p.perm, GrowsDown: p.growsDown,
			Anonymous: p.anon}
		if !p.anon {
			vma.Source = Source{File: p.file, Off: startPage.off}
		}
		out = append(out, vma)
	}
	for i := 0; i+1 < len(out); {
		if compatible(out[i], out[i+1]) {
			out[i].End = out[i+1].End
			out = append(out[:i+1], out[i+2:]...)
			continue
		}
		i++
	}
	return out
}

func (n *naiveManager) samePage(a, b naivePage) bool {
	if a.present != b.present || a.perm != b.perm || a.growsDown != b.growsDown || a.anon != b.anon {
		return false
	}
	return a.anon || (a.file == b.file && a.off == b.off)
}

func (n *naiveManager) blocked(addr int64) bool {
	if p := n.at(addr); p.present {
		return true
	}
	for s := addr + 1; s <= addr+n.guard && s < n.high; s++ {
		if p := n.at(s); p.present && p.stack {
			return true
		}
	}
	return false
}

func (n *naiveManager) count() int { return len(n.vmAs()) }

func (n *naiveManager) mmap(hint, length int64, perm, flags int, file, off int64) (int64, error) {
	if n.badMmap(hint, length, perm, flags, file, off) {
		return 0, invalidArgument()
	}
	if flags&Fixed != 0 {
		end := hint + length
		overlaps := false
		for a := hint; a < end; a++ {
			if n.at(a).present {
				if flags&NoReplace != 0 {
					return 0, ErrExists
				}
				overlaps = true
			}
		}
		if overlaps {
			before := n.vmAs()
			removed, splits, _, _ := removeRange(before, hint, end)
			if len(before)+splits > n.maxV {
				return 0, ErrTooMany
			}
			candidate := VMA{Start: hint, End: end, Perm: int(perm), GrowsDown: flags&GrowsDown != 0,
				Anonymous: file == 0}
			if file != 0 {
				candidate.Source = Source{File: file, Off: off}
			}
			_, merges := mergeInserted(removed, candidate)
			if merges == 0 && len(removed)+1 > n.maxV {
				return 0, ErrTooMany
			}
			for a := hint; a < end; a++ {
				n.pages[a-n.low] = naivePage{}
			}
		}
		current := n.vmAs()
		candidate := VMA{Start: hint, End: hint + length, Perm: int(perm), GrowsDown: flags&GrowsDown != 0,
			Anonymous: file == 0}
		if file != 0 {
			candidate.Source = Source{File: file, Off: off}
		}
		_, merges := mergeInserted(current, candidate)
		if merges == 0 && len(current)+1 > n.maxV {
			return 0, ErrTooMany
		}
		n.place(hint, length, perm, flags, file, off)
		return hint, nil
	}

	start := int64(-1)
	end := hint + length
	if hint != 0 && hint >= n.low && end <= n.high {
		ok := true
		for a := hint; a < end; a++ {
			if n.blocked(a) {
				ok = false
			}
		}
		if ok {
			start = hint
		}
	}
	if start < 0 {
		for candidate := n.high - length; candidate >= n.low; candidate-- {
			ok := true
			for a := candidate; a < candidate+length; a++ {
				if n.blocked(a) {
					ok = false
					break
				}
			}
			if ok {
				start = candidate
				break
			}
		}
	}
	if start < 0 {
		return 0, ErrNoSpace
	}
	current := n.vmAs()
	candidate := VMA{Start: start, End: start + length, Perm: int(perm), GrowsDown: flags&GrowsDown != 0,
		Anonymous: file == 0}
	if file != 0 {
		candidate.Source = Source{File: file, Off: off}
	}
	_, merges := mergeInserted(current, candidate)
	if merges == 0 && len(current)+1 > n.maxV {
		return 0, ErrTooMany
	}
	n.place(start, length, perm, flags, file, off)
	return start, nil
}

func (n *naiveManager) badMmap(hint, length int64, perm, flags int, file, off int64) bool {
	if length < 1 || perm < 0 || perm > 7 || flags < 0 || flags&^(Fixed|NoReplace|GrowsDown) != 0 ||
		(flags&NoReplace != 0 && flags&Fixed == 0) || (flags&GrowsDown != 0 && file != 0) ||
		file < 0 || off < 0 || hint < 0 {
		return true
	}
	return flags&Fixed != 0 && (hint < n.low || hint+length > n.high)
}

func (n *naiveManager) place(start, length int64, perm, flags int, file, off int64) {
	for i := int64(0); i < length; i++ {
		p := n.at(start + i)
		p.present, p.perm, p.growsDown, p.stack = true, perm, flags&GrowsDown != 0, flags&GrowsDown != 0
		p.anon = file == 0
		p.file, p.off = file, off+i
	}
}

func (n *naiveManager) munmap(start, length int64) (int64, error) {
	if length < 1 || start < n.low || start+length > n.high {
		return 0, invalidArgument()
	}
	splits, pages := 0, int64(0)
	current := n.vmAs()
	if !intersectsAny(current, start, start+length) {
		return 0, nil
	}
	for _, v := range current {
		if intervalsOverlap(v.Start, v.End, start, start+length) {
			parts := clipVMA(v, start, start+length)
			splits += len(parts) - 1
			pages += minInt64(v.End, start+length) - maxInt64(v.Start, start)
		}
	}
	if len(current)+splits > n.maxV {
		return 0, ErrTooMany
	}
	for a := start; a < start+length; a++ {
		n.pages[a-n.low] = naivePage{}
	}
	return pages, nil
}

func (n *naiveManager) mprotect(start, length, perm int64) error {
	if length < 1 || perm < 0 || perm > 7 || start < n.low || start+length > n.high {
		return invalidArgument()
	}
	end := start + length
	for a := start; a < end; a++ {
		if !n.at(a).present {
			return ErrNoMem
		}
	}
	current := n.vmAs()
	splits := 0
	changed := false
	for _, v := range current {
		if intervalsOverlap(v.Start, v.End, start, end) && v.Perm != int(perm) {
			changed = true
			splits += len(clipVMA(v, start, end)) - 1
		}
	}
	if !changed {
		return nil
	}
	if len(current)+splits > n.maxV {
		return ErrTooMany
	}
	for a := start; a < end; a++ {
		n.at(a).perm = int(perm)
	}
	return nil
}

func (n *naiveManager) grow(addr int64) (int64, error) {
	if addr < n.low || addr >= n.high {
		return 0, invalidArgument()
	}
	if n.at(addr).present {
		return 0, ErrMapped
	}
	var stack *VMA
	for _, v := range n.vmAs() {
		if v.Start > addr {
			stack = &v
			break
		}
	}
	if stack == nil || !stack.GrowsDown {
		return 0, ErrSegv
	}
	if stack.End-addr > n.maxStack {
		return 0, ErrStackLimit
	}
	lowerEnd := n.low
	for a := addr - 1; a >= n.low; a-- {
		if n.at(a).present {
			lowerEnd = a + 1
			break
		}
	}
	if addr-lowerEnd < n.guard {
		return 0, ErrNoRoom
	}
	for a := addr; a < stack.Start; a++ {
		p := n.at(a)
		*p = *n.at(stack.Start)
		p.off = p.off - (stack.Start - a)
		p.stack = true
	}
	return addr, nil
}

type randomOp struct {
	kind   string
	hint   int64
	length int64
	perm   int
	flags  int
	file   int64
	off    int64
	addr   int64
}

func TestRandomVsNaive(t *testing.T) {
	seed := int64(1)
	if !testing.Short() {
		seed = 2
	}
	for iteration := 0; iteration < 2000; iteration++ {
		rng := rand.New(rand.NewSource(seed + int64(iteration)*1000003))
		low := int64(1)
		high := int64(8 + rng.Intn(30))
		maxV := 1 + rng.Intn(12)
		guard := int64(1 + rng.Intn(4))
		stackMax := int64(3 + rng.Intn(20))
		m, err := New(low, high, maxV, guard, stackMax)
		if err != nil {
			t.Fatal(err)
		}
		nm := newNaive(low, high, maxV, guard, stackMax)
		var log []randomOp
		for step := 0; step < 25; step++ {
			op := randomOp{kind: []string{"mmap", "munmap", "mprotect", "grow"}[rng.Intn(4)]}
			switch op.kind {
			case "mmap":
				op.length = int64(1 + rng.Intn(int(high-low)+5))
				op.perm = rng.Intn(9)
				op.flags = rng.Intn(8)
				if rng.Intn(3) == 0 {
					op.flags |= Fixed
				}
				if op.flags&Fixed == 0 {
					op.flags &^= NoReplace
				}
				if rng.Intn(2) == 0 {
					op.flags |= GrowsDown
				}
				if op.flags&GrowsDown == 0 && rng.Intn(3) == 0 {
					op.file = int64(1 + rng.Intn(3))
					op.off = int64(rng.Intn(20))
				}
				if op.flags&Fixed != 0 {
					op.hint = low + int64(rng.Intn(int(high-low)))
				} else {
					op.hint = int64(rng.Intn(int(high-low)+2)) - 1
				}
			case "munmap", "mprotect":
				op.addr = low + int64(rng.Intn(int(high-low)))
				op.length = int64(1 + rng.Intn(int(high-op.addr)+3))
				op.perm = rng.Intn(9)
			case "grow":
				op.addr = low + int64(rng.Intn(int(high-low)))
			}
			log = append(log, op)
			runAndCompare(t, iteration, step, m, nm, op)
		}
	}
}

func runAndCompare(t *testing.T, iteration, step int, m *Manager, nm *naiveManager, op randomOp) {
	t.Helper()
	t.Logf("iter=%d step=%d op=%+v actual=%v naive=%v", iteration, step, op, m.VMAs(), nm.vmAs())
	switch op.kind {
	case "mmap":
		a, ea := m.Mmap(op.hint, op.length, op.perm, op.flags, op.file, op.off)
		b, eb := nm.mmap(op.hint, op.length, op.perm, op.flags, op.file, op.off)
		if a != b || fmt.Sprint(ea) != fmt.Sprint(eb) {
			t.Fatalf("iter=%d step=%d mmap mismatch got=(%d,%v) want=(%d,%v) op=%+v\nactual=%#v\nnaive=%#v",
				iteration, step, a, ea, b, eb, op, m.VMAs(), nm.vmAs())
		}
	case "munmap":
		a, ea := m.Munmap(op.addr, op.length)
		b, eb := nm.munmap(op.addr, op.length)
		if a != b || fmt.Sprint(ea) != fmt.Sprint(eb) {
			t.Fatalf("iter=%d step=%d munmap mismatch got=(%d,%v) want=(%d,%v) op=%+v", iteration, step, a, ea, b, eb, op)
		}
	case "mprotect":
		ea := m.Mprotect(op.addr, op.length, op.perm)
		eb := nm.mprotect(op.addr, op.length, int64(op.perm))
		if fmt.Sprint(ea) != fmt.Sprint(eb) {
			t.Fatalf("iter=%d step=%d mprotect mismatch got=%v want=%v op=%+v", iteration, step, ea, eb, op)
		}
	case "grow":
		a, ea := m.Grow(op.addr)
		b, eb := nm.grow(op.addr)
		if a != b || fmt.Sprint(ea) != fmt.Sprint(eb) {
			t.Fatalf("iter=%d step=%d grow mismatch got=(%d,%v) want=(%d,%v) op=%+v", iteration, step, a, ea, b, eb, op)
		}
	}
	if got, want := m.VMAs(), nm.vmAs(); !vmaSlicesEqual(got, want) {
		t.Fatalf("iter=%d step=%d tables differ after op=%+v\ngot=%#v\nwant=%#v", iteration, step, op, got, want)
	}
}

func vmaSlicesEqual(a, b []VMA) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
