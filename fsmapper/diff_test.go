package fsmapper

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

// 随机合法源名的字符池。
var nameRunes = []rune{
	'a', 'b', 'c', 'A', 'B', 'C', '1', '2', '.', ' ',
	':', '?', '*', '%', '<', '>', '|', '"', '\\',
	'中', '文', 'é',
}

func randSrc(rng *rand.Rand) string {
	length := 1 + rng.IntN(24)
	var sb strings.Builder
	for i := 0; i < length; i++ {
		if rng.IntN(40) == 0 {
			sb.WriteByte(byte(1 + rng.IntN(0x1F)))
			continue
		}
		sb.WriteRune(nameRunes[rng.IntN(len(nameRunes))])
	}
	if rng.IntN(6) == 0 {
		if rng.IntN(2) == 0 {
			sb.WriteByte('.')
		} else {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

func maybeInvalid(rng *rand.Rand) (string, bool) {
	switch rng.IntN(12) {
	case 0:
		return "", true
	case 1:
		return "a/b", true
	case 2:
		return "a\x00b", true
	case 3:
		return "bad\xff", true
	}
	return "", false
}

type opKind int

const (
	opAdd opKind = iota
	opRemove
	opRename
	opLookup
	opPath
	opNames
)

func snapshotOf(m *Mapper) []string {
	var ids []int64
	for id := range m.entries {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		e := m.entries[id]
		p, _ := m.Path(id)
		out = append(out, fmt.Sprintf("%d:%s|%s|dir=%v|len=%d",
			id, e.src, p, e.isDir, e.pathLen))
	}
	return out
}

func errLabel(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) || errors.Is(b, a) || a.Error() == b.Error()
}

func removeInt64(s []int64, v int64) []int64 {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// TestRandomDifferential：与朴素实现对照 2000 组随机树操作，
// 日志逐条打印输入、输出与判定依据（go test -v 查看）。
func TestRandomDifferential(t *testing.T) {
	const groups = 2000
	for g := 1; g <= groups; g++ {
		t.Run(fmt.Sprintf("g%d", g), func(t *testing.T) {
			runOneDifferential(t, uint64(g))
		})
	}
}

func runOneDifferential(t *testing.T, seed uint64) {
	const steps = 80
	rng := rand.New(rand.NewPCG(seed, 20261003))

	mb := 16 + rng.IntN(40)
	mp := mb + rng.IntN(80)
	me := 1 + rng.IntN(30)

	m, err := New(mb, mp, me)
	if err != nil {
		t.Fatal(err)
	}
	nv := newNaive(mb, mp, me)
	t.Logf("CONFIG MaxBytes=%d MaxPath=%d MaxEntries=%d", mb, mp, me)

	type live struct {
		id, parent int64
		src        string
		isDir      bool
	}
	alive := map[int64]*live{}
	dirIDs := []int64{0}

	chooseDir := func() int64 { return dirIDs[rng.IntN(len(dirIDs))] }
	srcInDir := func(parent int64) (string, int64, bool) {
		var cands []*live
		for _, e := range alive {
			if e.parent == parent {
				cands = append(cands, e)
			}
		}
		if len(cands) == 0 {
			return "", 0, false
		}
		e := cands[rng.IntN(len(cands))]
		return e.src, e.id, true
	}

	for step := 1; step <= steps; step++ {
		kind := opKind(rng.IntN(int(opNames + 1)))
		if len(alive) < 3 {
			kind = opAdd
		}

		switch kind {
		case opAdd:
			parent := chooseDir()
			src := randSrc(rng)
			isDir := rng.IntN(3) == 0
			if s, bad := maybeInvalid(rng); bad {
				src = s
			}
			id1, e1 := m.Add(parent, src, isDir)
			id2, e2 := nv.Add(parent, src, isDir)
			t.Logf("step %d Add(parent=%d src=%q isDir=%v) -> id=%d err=%s | naive id=%d err=%s; 判定: 优先级 Invalid>NoParent>Exists>Full>CannotFit>PathTooLong",
				step, parent, src, isDir, id1, errLabel(e1), id2, errLabel(e2))
			if !sameErr(e1, e2) || (e1 == nil && id1 != id2) {
				t.Fatalf("step %d Add mismatch: (%d,%v) vs (%d,%v)", step, id1, e1, id2, e2)
			}
			if e1 == nil {
				alive[id1] = &live{id1, parent, src, isDir}
				if isDir {
					dirIDs = append(dirIDs, id1)
				}
			}

		case opRemove:
			parent := chooseDir()
			src, id, ok := srcInDir(parent)
			if !ok || rng.IntN(3) == 0 {
				src = randSrc(rng)
				ok = false
			}
			e1 := m.Remove(parent, src)
			e2 := nv.Remove(parent, src)
			t.Logf("step %d Remove(parent=%d src=%q) -> err=%s | naive err=%s; 判定: NotFound / 非空 NotEmpty",
				step, parent, src, errLabel(e1), errLabel(e2))
			if !sameErr(e1, e2) {
				t.Fatalf("step %d Remove mismatch: %v vs %v", step, e1, e2)
			}
			if e1 == nil {
				if e, known := alive[id]; known && e.src == src {
					delete(alive, id)
					if e.isDir {
						dirIDs = removeInt64(dirIDs, id)
					}
				}
			}

		case opRename:
			parent := chooseDir()
			src, id, ok := srcInDir(parent)
			if !ok {
				src = randSrc(rng)
			}
			newSrc := randSrc(rng)
			if s, bad := maybeInvalid(rng); bad {
				newSrc = s
			}
			if rng.IntN(8) == 0 {
				newSrc = src
			}
			e1 := m.Rename(parent, src, newSrc)
			e2 := nv.Rename(parent, src, newSrc)
			t.Logf("step %d Rename(parent=%d src=%q newSrc=%q) -> err=%s | naive err=%s; 判定: 先释放后分配，Exists 按源名敏感，失败回滚",
				step, parent, src, newSrc, errLabel(e1), errLabel(e2))
			if !sameErr(e1, e2) {
				t.Fatalf("step %d Rename mismatch: %v vs %v", step, e1, e2)
			}
			if e1 == nil {
				if e, known := alive[id]; known {
					e.src = newSrc
				}
			}

		case opLookup:
			parent := chooseDir()
			src, _, _ := srcInDir(parent)
			if rng.IntN(2) == 0 {
				src = randSrc(rng)
			}
			id1, d1, e1 := m.Lookup(parent, src)
			id2, d2, e2 := nv.Lookup(parent, src)
			t.Logf("step %d Lookup(parent=%d src=%q) -> id=%d isDir=%v err=%s | naive id=%d isDir=%v err=%s",
				step, parent, src, id1, d1, errLabel(e1), id2, d2, errLabel(e2))
			if !sameErr(e1, e2) || id1 != id2 || d1 != d2 {
				t.Fatalf("step %d Lookup mismatch: (%d,%v,%v) vs (%d,%v,%v)",
					step, id1, d1, e1, id2, d2, e2)
			}

		case opPath:
			var id int64
			if rng.IntN(5) == 0 || len(alive) == 0 {
				id = int64(100000 + rng.IntN(10))
			} else {
				k := rng.IntN(len(alive))
				for cand := range alive {
					if k == 0 {
						id = cand
						break
					}
					k--
				}
			}
			p1, e1 := m.Path(id)
			p2, e2 := nv.Path(id)
			t.Logf("step %d Path(id=%d) -> %q err=%s | naive %q err=%s",
				step, id, p1, errLabel(e1), p2, errLabel(e2))
			if !sameErr(e1, e2) || p1 != p2 {
				t.Fatalf("step %d Path mismatch: (%q,%v) vs (%q,%v)", step, p1, e1, p2, e2)
			}

		case opNames:
			parent := chooseDir()
			n1, e1 := m.Names(parent)
			n2, e2 := nv.Names(parent)
			t.Logf("step %d Names(parent=%d) -> %v err=%s | naive %v err=%s; 判定: 映射名字节序",
				step, parent, n1, errLabel(e1), n2, errLabel(e2))
			if !sameErr(e1, e2) {
				t.Fatalf("step %d Names err mismatch: %v vs %v", step, e1, e2)
			}
			if len(n1) != len(n2) {
				t.Fatalf("step %d Names len mismatch: %v vs %v", step, n1, n2)
			}
			for i := range n1 {
				if n1[i] != n2[i] {
					t.Fatalf("step %d Names content mismatch: %v vs %v", step, n1, n2)
				}
			}
		}

		s1 := snapshotOf(m)
		s2 := nv.snapshot()
		if len(s1) != len(s2) {
			t.Fatalf("step %d snapshot len %d vs %d\nprod:%v\nnaive:%v",
				step, len(s1), len(s2), s1, s2)
		}
		for i := range s1 {
			if s1[i] != s2[i] {
				t.Fatalf("step %d snapshot row %d mismatch:\nprod: %s\nnaive:%s",
					step, i, s1[i], s2[i])
			}
		}
	}

	for dir, ds := range m.dirs {
		seen := map[string]bool{}
		for _, id := range ds.bySrc {
			e := m.entries[id]
			if len(e.mapped) > mb {
				t.Fatalf("dir %d: %q exceeds MaxBytes", dir, e.mapped)
			}
			if e.pathLen > mp {
				t.Fatalf("dir %d: path %q exceeds MaxPath", dir, e.mapped)
			}
			k := asciiFold(e.mapped)
			if seen[k] {
				t.Fatalf("dir %d: duplicate fold key %q", dir, k)
			}
			seen[k] = true
		}
	}
	t.Logf("DIFFERENTIAL OK: %d operations, final entries=%d", steps, len(alive))
}
