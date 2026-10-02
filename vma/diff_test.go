package vma

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type opKind int

const (
	opMmap opKind = iota
	opMunmap
	opMprotect
	opGrow
	opFind
)

type op struct {
	kind         opKind
	hint, length int64
	perm, flags  int
	file, off    int64
	addr         int64
}

func validateMmap(low, high int64, o op) bool {
	fixed := o.flags&FlagFixed != 0
	if o.length < 1 || o.perm < 0 || o.perm > 7 || o.flags&^allFlags != 0 {
		return false
	}
	if o.flags&FlagNoReplace != 0 && !fixed {
		return false
	}
	if o.flags&FlagGrowsDown != 0 && o.file != 0 {
		return false
	}
	if o.file < 0 || o.off < 0 || o.hint < 0 {
		return false
	}
	if fixed && (o.hint < low || o.hint+o.length > high || o.hint+o.length < o.hint) {
		return false
	}
	return true
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

func tablesEqual(a, b []VMA) bool {
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

func runDiff(t *testing.T, seed int64, nops, span int, maxv int, guard, sm int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	low := int64(1)
	high := int64(span)

	m, err := New(low, high, maxv, guard, sm)
	if err != nil {
		t.Fatal(err)
	}
	nm := newNaive(low, high, maxv, guard, sm)

	var log []string
	failf := func(format string, args ...interface{}) {
		t.Fatalf("seed=%d\n%s\n%s", seed, joinLines(log), fmt.Sprintf(format, args...))
	}

	for step := 0; step < nops; step++ {
		var o op
		k := rng.Intn(100)
		switch {
		case k < 55:
			o.kind = opMmap
			o.length = int64(1 + rng.Intn(int(high-low)+2))
			o.perm = rng.Intn(8)
			o.flags = 0
			if rng.Intn(100) < 12 {
				o.flags |= FlagFixed
				o.hint = low + int64(rng.Intn(int(high-low)))
				if o.hint+o.length > high {
					o.length = high - o.hint
				}
				if rng.Intn(100) < 20 {
					o.flags |= FlagNoReplace
				}
			} else {
				switch rng.Intn(100) {
				case 0:
					o.hint = 0
				case 1:
					o.hint = low + int64(rng.Intn(int(high-low)))
				default:
					// Biased hint near the top to exercise hint acceptance.
					o.hint = low + int64(rng.Intn(int(high-low)))
				}
			}
			if rng.Intn(100) < 15 {
				o.flags |= FlagGrowsDown
			}
			if rng.Intn(100) < 35 && o.flags&FlagGrowsDown == 0 {
				o.file = int64(1 + rng.Intn(3))
				o.off = int64(rng.Intn(int(high - low)))
			}
			if !validateMmap(low, high, o) {
				step--
				continue
			}
		case k < 75:
			o.kind = opMunmap
			o.addr = low + int64(rng.Intn(int(high-low)))
			o.length = int64(1 + rng.Intn(int(high-o.addr)))
		case k < 92:
			o.kind = opMprotect
			o.addr = low + int64(rng.Intn(int(high-low)))
			o.length = int64(1 + rng.Intn(int(high-o.addr)))
			o.perm = rng.Intn(8)
		default:
			o.kind = opGrow
			o.addr = low + int64(rng.Intn(int(high-low)))
		}

		switch o.kind {
		case opMmap:
			got, gerr := m.Mmap(o.hint, o.length, o.perm, o.flags, o.file, o.off)
			want, werr := nm.mmap(o.hint, o.length, o.perm, o.flags, o.file, o.off)
			log = append(log, fmt.Sprintf("step=%d MMAP hint=%d len=%d perm=%d flags=%d file=%d off=%d -> got=(%d,%v) want=(%d,%v)",
				step, o.hint, o.length, o.perm, o.flags, o.file, o.off, got, gerr, want, werr))
			for _, l := range nm.log {
				log = append(log, "    naive: "+l)
			}
			nm.log = nil
			if !sameErr(gerr, werr) || (gerr == nil && got != want) {
				failf("MMAP mismatch: got=(%d,%v) want=(%d,%v)\ngot table=%v\nwant table=%v",
					got, gerr, want, werr, m.VMAs(), nm.table())
			}
		case opMunmap:
			got, gerr := m.Munmap(o.addr, o.length)
			want, werr := nm.munmap(o.addr, o.length)
			log = append(log, fmt.Sprintf("step=%d MUNMAP %d len=%d -> got=(%d,%v) want=(%d,%v)",
				step, o.addr, o.length, got, gerr, want, werr))
			for _, l := range nm.log {
				log = append(log, "    naive: "+l)
			}
			nm.log = nil
			if !sameErr(gerr, werr) || (gerr == nil && got != want) {
				failf("MUNMAP mismatch: got=(%d,%v) want=(%d,%v)", got, gerr, want, werr)
			}
		case opMprotect:
			gerr := m.Mprotect(o.addr, o.length, o.perm)
			werr := nm.mprotect(o.addr, o.length, int64(o.perm))
			log = append(log, fmt.Sprintf("step=%d MPROTECT %d len=%d perm=%d -> got=%v want=%v",
				step, o.addr, o.length, o.perm, gerr, werr))
			for _, l := range nm.log {
				log = append(log, "    naive: "+l)
			}
			nm.log = nil
			if !sameErr(gerr, werr) {
				failf("MPROTECT mismatch: got=%v want=%v\ngot table=%v\nwant table=%v",
					gerr, werr, m.VMAs(), nm.table())
			}
		case opGrow:
			got, gerr := m.Grow(o.addr)
			want, werr := nm.grow(o.addr)
			log = append(log, fmt.Sprintf("step=%d GROW %d -> got=(%d,%v) want=(%d,%v)",
				step, o.addr, got, gerr, want, werr))
			for _, l := range nm.log {
				log = append(log, "    naive: "+l)
			}
			nm.log = nil
			if !sameErr(gerr, werr) || (gerr == nil && got != want) {
				failf("GROW mismatch: got=(%d,%v) want=(%d,%v)", got, gerr, want, werr)
			}
		}

		gt, wt := m.VMAs(), nm.table()
		if !tablesEqual(gt, wt) {
			failf("table mismatch\ngot=%v\nwant=%v", gt, wt)
		}
		if m.Count() != nm.count() {
			failf("count mismatch got=%d want=%d", m.Count(), nm.count())
		}
	}
}

func joinLines(ls []string) string {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return out
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for i := 0; i < sequences; i++ {
		seed := int64(1000 + i)
		rng := rand.New(rand.NewSource(seed))
		span := 8 + rng.Intn(40)
		maxv := 1 + rng.Intn(12)
		guard := int64(1 + rng.Intn(5))
		sm := int64(1 + rng.Intn(30))
		nops := 40 + rng.Intn(120)
		runDiff(t, seed, nops, span, maxv, guard, sm)
	}
}
