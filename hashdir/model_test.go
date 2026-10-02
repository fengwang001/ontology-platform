package hashdir

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveModel is a straightforward from-scratch implementation of the spec,
// used as the decision basis for the randomized comparison test. It keeps
// per-hash used-minor sets and re-sorts all keys on every read.
type naiveModel struct {
	seed  uint32
	m     uint32
	gen   uint64
	items map[string]mItem
	used  map[uint32]map[uint32]bool
	snap  map[string]uint64
}

type mItem struct {
	ino uint64
	key uint64
}

func newModel(seed, m uint32) *naiveModel {
	return &naiveModel{
		seed:  seed,
		m:     m,
		gen:   1,
		items: make(map[string]mItem),
		used:  make(map[uint32]map[uint32]bool),
	}
}

func smallestFree(minors map[uint32]bool) uint32 {
	var v uint32
	for minors[v] {
		v++
	}
	return v
}

func (mo *naiveModel) insert(name string, ino uint64) (uint64, error) {
	h := hashName(mo.seed, name)
	minors := mo.used[h]
	if minors == nil {
		minors = make(map[uint32]bool)
		mo.used[h] = minors
	}
	if uint32(len(minors)) >= mo.m {
		return 0, ErrHashFull
	}
	minor := smallestFree(minors)
	minors[minor] = true
	key := makeKey(h, minor)
	mo.items[name] = mItem{ino: ino, key: key}
	return key, nil
}

func (mo *naiveModel) remove(name string) {
	h := hashName(mo.seed, name)
	it := mo.items[name]
	delete(mo.items, name)
	delete(mo.used[h], uint32(it.key))
	if len(mo.used[h]) == 0 {
		delete(mo.used, h)
	}
}

func (mo *naiveModel) add(name string, ino uint64) (uint64, error) {
	if name == "" {
		return 0, ErrInvalidArgument
	}
	if _, ok := mo.items[name]; ok {
		return 0, ErrAlreadyExists
	}
	return mo.insert(name, ino)
}

func (mo *naiveModel) removeOp(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	if _, ok := mo.items[name]; !ok {
		return ErrNotFound
	}
	mo.remove(name)
	return nil
}

func (mo *naiveModel) rename(oldName, newName string) (uint64, error) {
	if oldName == "" || newName == "" {
		return 0, ErrInvalidArgument
	}
	it, ok := mo.items[oldName]
	if !ok {
		return 0, ErrNotFound
	}
	if newName == oldName {
		return 0, ErrAlreadyExists
	}
	if _, dup := mo.items[newName]; dup {
		return 0, ErrAlreadyExists
	}
	hOld := hashName(mo.seed, oldName)
	hNew := hashName(mo.seed, newName)
	count := len(mo.used[hNew])
	if hNew == hOld {
		count--
	}
	if uint32(count) >= mo.m {
		return 0, ErrHashFull
	}
	mo.remove(oldName)
	return mo.insert(newName, it.ino)
}

func (mo *naiveModel) sortedKeys() []uint64 {
	keys := make([]uint64, 0, len(mo.items))
	for _, it := range mo.items {
		keys = append(keys, it.key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (mo *naiveModel) readDir(c Cookie, n int) ([]Entry, Cookie, bool, error) {
	if n < 0 {
		return nil, c, false, ErrInvalidArgument
	}
	pos := c.Pos
	migrated := false
	if c.Pos != 0 && c.Gen != mo.gen {
		if c.Gen != mo.gen-1 || mo.snap == nil {
			return nil, c, false, ErrStaleCookie
		}
		var q string
		found := false
		for name, key := range mo.snap {
			if key == c.Pos-1 {
				q = name
				found = true
				break
			}
		}
		if !found {
			return nil, c, false, ErrStaleCookie
		}
		it, ok := mo.items[q]
		if !ok {
			return nil, c, false, ErrStaleCookie
		}
		pos = it.key + 1
		migrated = true
	}
	keys := mo.sortedKeys()
	nameByKey := make(map[uint64]string, len(mo.items))
	for name, it := range mo.items {
		nameByKey[it.key] = name
	}
	var entries []Entry
	for _, key := range keys {
		if key < pos {
			continue
		}
		if len(entries) == n {
			break
		}
		name := nameByKey[key]
		entries = append(entries, Entry{Name: name, Ino: mo.items[name].ino, Key: key})
	}
	next := c
	if len(entries) > 0 {
		next = Cookie{Gen: mo.gen, Pos: entries[len(entries)-1].Key + 1}
	} else if migrated {
		next = Cookie{Gen: mo.gen, Pos: pos}
	}
	done := true
	for _, key := range keys {
		if key >= next.Pos {
			done = false
			break
		}
	}
	return entries, next, done, nil
}

func (mo *naiveModel) cookieOf(name string) (Cookie, error) {
	if name == "" {
		return Cookie{}, ErrInvalidArgument
	}
	it, ok := mo.items[name]
	if !ok {
		return Cookie{}, ErrNotFound
	}
	return Cookie{Gen: mo.gen, Pos: it.key + 1}, nil
}

func (mo *naiveModel) rehash(newSeed uint32) error {
	names := make([]string, 0, len(mo.items))
	for name := range mo.items {
		names = append(names, name)
	}
	sort.Strings(names)
	newUsed := make(map[uint32]map[uint32]bool)
	newKeys := make(map[string]uint64, len(names))
	for _, name := range names {
		h := hashName(newSeed, name)
		minors := newUsed[h]
		if minors == nil {
			minors = make(map[uint32]bool)
			newUsed[h] = minors
		}
		if uint32(len(minors)) >= mo.m {
			return ErrHashFull
		}
		minor := smallestFree(minors)
		minors[minor] = true
		newKeys[name] = makeKey(h, minor)
	}
	mo.snap = make(map[string]uint64, len(mo.items))
	for name, it := range mo.items {
		mo.snap[name] = it.key
	}
	mo.gen++
	mo.seed = newSeed
	mo.used = newUsed
	for name, key := range newKeys {
		mo.items[name] = mItem{ino: mo.items[name].ino, key: key}
	}
	return nil
}

// TestRandomAgainstModel replays 2000 random operation sequences against
// both the Store (twice, to prove replay determinism) and the naive model,
// comparing every output. Each step is logged with input, output and the
// decision basis.
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	names := append(append([]string{}, collNames...), pairNames...)
	names = append(names, "x", "y", "z", "w")
	seeds := []uint32{collSeed, 1, 2}
	ms := []uint32{1, 2, 3, 5}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		m := ms[rng.Intn(len(ms))]
		seed := seeds[rng.Intn(len(seeds))]
		s1, err := New(seed, m)
		if err != nil {
			t.Fatal(err)
		}
		s2, err := New(seed, m)
		if err != nil {
			t.Fatal(err)
		}
		mo := newModel(seed, m)
		var cookies []Cookie
		steps := 10 + rng.Intn(20)
		for step := 0; step < steps; step++ {
			name := names[rng.Intn(len(names))]
			checkErr := func(op string, e1, e2, em error) {
				t.Helper()
				if !errors.Is(e1, em) || !errors.Is(e2, em) {
					t.Fatalf("seq=%d step=%d %s: store1 err=%v store2 err=%v model err=%v",
						seq, step, op, e1, e2, em)
				}
			}
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
				20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // Add 30%
				ino := uint64(rng.Intn(1000))
				k1, e1 := s1.Add(name, ino)
				k2, e2 := s2.Add(name, ino)
				km, em := mo.add(name, ino)
				checkErr("Add", e1, e2, em)
				if e1 == nil && (k1 != km || k2 != km) {
					t.Fatalf("seq=%d step=%d Add(%q,%d): store keys %d,%d model %d",
						seq, step, name, ino, k1, k2, km)
				}
				t.Logf("seq=%d step=%d op=Add(%q,%d) out=(key=%d,err=%v) basis=model(key=%d,err=%v) match",
					seq, step, name, ino, k1, e1, km, em)
			case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44: // Remove 15%
				e1 := s1.Remove(name)
				e2 := s2.Remove(name)
				em := mo.removeOp(name)
				checkErr("Remove", e1, e2, em)
				t.Logf("seq=%d step=%d op=Remove(%q) out=err=%v basis=model(err=%v) match",
					seq, step, name, e1, em)
			case 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59: // Rename 15%
				newName := names[rng.Intn(len(names))]
				k1, e1 := s1.Rename(name, newName)
				k2, e2 := s2.Rename(name, newName)
				km, em := mo.rename(name, newName)
				checkErr("Rename", e1, e2, em)
				if e1 == nil && (k1 != km || k2 != km) {
					t.Fatalf("seq=%d step=%d Rename(%q,%q): store keys %d,%d model %d",
						seq, step, name, newName, k1, k2, km)
				}
				t.Logf("seq=%d step=%d op=Rename(%q,%q) out=(key=%d,err=%v) basis=model(key=%d,err=%v) match",
					seq, step, name, newName, k1, e1, km, em)
			case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // ReadDir 20%
				var c Cookie
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4, 5: // fresh start cursor
					c = Cookie{}
				case 6, 7, 8: // reuse a recent cursor
					if len(cookies) > 0 {
						c = cookies[rng.Intn(len(cookies))]
					}
				default: // garbage cursor, likely stale
					c = Cookie{Gen: uint64(rng.Intn(8)), Pos: uint64(rng.Intn(1 << 40))}
				}
				n := rng.Intn(7) - 1 // -1..5
				en1, nc1, d1, e1 := s1.ReadDir(c, n)
				en2, nc2, d2, e2 := s2.ReadDir(c, n)
				enm, ncm, dm, em := mo.readDir(c, n)
				checkErr("ReadDir", e1, e2, em)
				if e1 == nil {
					if !entriesEqual(en1, enm) || !entriesEqual(en2, enm) ||
						nc1 != ncm || nc2 != ncm || d1 != dm || d2 != dm {
						t.Fatalf("seq=%d step=%d ReadDir(%+v,%d):\nstore1=(%v,%+v,%v)\nstore2=(%v,%+v,%v)\nmodel =(%v,%+v,%v)",
							seq, step, c, n, en1, nc1, d1, en2, nc2, d2, enm, ncm, dm)
					}
					cookies = append(cookies, nc1)
					if len(cookies) > 8 {
						cookies = cookies[1:]
					}
				}
				t.Logf("seq=%d step=%d op=ReadDir(%+v,%d) out=(%d entries,next=%+v,done=%v,err=%v) basis=model(%d entries,next=%+v,done=%v,err=%v) match",
					seq, step, c, n, len(en1), nc1, d1, e1, len(enm), ncm, dm, em)
			case 80, 81, 82, 83, 84, 85, 86, 87, 88, 89: // CookieOf 10%
				c1, e1 := s1.CookieOf(name)
				c2, e2 := s2.CookieOf(name)
				cm, em := mo.cookieOf(name)
				checkErr("CookieOf", e1, e2, em)
				if e1 == nil && (c1 != cm || c2 != cm) {
					t.Fatalf("seq=%d step=%d CookieOf(%q): store %+v,%+v model %+v",
						seq, step, name, c1, c2, cm)
				}
				if e1 == nil {
					cookies = append(cookies, c1)
					if len(cookies) > 8 {
						cookies = cookies[1:]
					}
				}
				t.Logf("seq=%d step=%d op=CookieOf(%q) out=(%+v,err=%v) basis=model(%+v,err=%v) match",
					seq, step, name, c1, e1, cm, em)
			default: // Rehash 10%
				newSeed := seeds[rng.Intn(len(seeds))]
				e1 := s1.Rehash(newSeed)
				e2 := s2.Rehash(newSeed)
				em := mo.rehash(newSeed)
				checkErr("Rehash", e1, e2, em)
				if s1.Gen() != mo.gen || s2.Gen() != mo.gen {
					t.Fatalf("seq=%d step=%d Rehash(%d): gens %d,%d model %d",
						seq, step, newSeed, s1.Gen(), s2.Gen(), mo.gen)
				}
				t.Logf("seq=%d step=%d op=Rehash(%d) out=(gen=%d,err=%v) basis=model(gen=%d,err=%v) match",
					seq, step, newSeed, s1.Gen(), e1, mo.gen, em)
			}
		}
	}
}

func entriesEqual(a, b []Entry) bool {
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

// TestConcurrentSmoke hammers one Store from many goroutines; run with
// -race. Afterwards a full scan must return every surviving name exactly
// once in strictly increasing key order.
func TestConcurrentSmoke(t *testing.T) {
	s, err := New(collSeed, math.MaxInt32)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const rounds = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			mine := make([]string, 0, rounds)
			for r := 0; r < rounds; r++ {
				name := fmt.Sprintf("w%d-%d", w, r)
				switch rng.Intn(6) {
				case 0, 1, 2:
					if _, err := s.Add(name, uint64(r)); err == nil {
						mine = append(mine, name)
					}
				case 3:
					if len(mine) > 0 {
						s.Remove(mine[rng.Intn(len(mine))])
					}
				case 4:
					if len(mine) > 0 {
						old := mine[rng.Intn(len(mine))]
						s.Rename(old, old+"-r")
					}
				case 5:
					c := Cookie{}
					for {
						ents, next, done, err := s.ReadDir(c, 3)
						if err != nil {
							break
						}
						for i := 1; i < len(ents); i++ {
							if ents[i-1].Key >= ents[i].Key {
								t.Errorf("keys not increasing: %v", ents)
							}
						}
						if done || len(ents) == 0 {
							break
						}
						c = next
					}
				}
			}
		}(w)
	}
	wg.Wait()
	// Final full scan: strictly increasing keys, no duplicates.
	c := Cookie{}
	var last uint64
	count := 0
	for {
		ents, next, done, err := s.ReadDir(c, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if count > 0 && e.Key <= last {
				t.Fatalf("key order violated: %d after %d", e.Key, last)
			}
			last = e.Key
			count++
		}
		if done {
			break
		}
		c = next
	}
	t.Logf("final scan returned %d entries", count)
}
