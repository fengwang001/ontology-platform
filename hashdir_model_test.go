package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type modelKey struct {
	h     uint32
	minor uint32
}

type modelEntry struct {
	name string
	ino  uint64
	key  modelKey
}

type modelItem struct {
	ino uint64
	key modelKey
}

type modelState struct {
	seed    uint32
	limit   int
	gen     uint64
	items   map[string]modelItem
	prevGen uint64
	prev    map[string]modelKey
	log     []string
}

func newModel(seed uint32, limit int) *modelState {
	return &modelState{seed: seed, limit: limit, gen: 1, items: map[string]modelItem{}}
}

func (m *modelState) logf(format string, args ...any) {
	m.log = append(m.log, fmt.Sprintf(format, args...))
}

func modelKeyUint64(key modelKey) uint64 {
	return uint64(key.h)<<32 | uint64(key.minor)
}

func (m *modelState) bucket(h uint32) map[uint32]string {
	result := make(map[uint32]string)
	for name := range m.items {
		item := m.items[name]
		if item.key.h == h {
			result[item.key.minor] = name
		}
	}
	return result
}

func (m *modelState) sorted(h uint32) []string {
	bucket := m.bucket(h)
	minors := make([]uint32, 0, len(bucket))
	for minor := range bucket {
		minors = append(minors, minor)
	}
	sort.Slice(minors, func(i, j int) bool { return minors[i] < minors[j] })
	names := make([]string, 0, len(minors))
	for _, minor := range minors {
		names = append(names, bucket[minor])
	}
	return names
}

func (m *modelState) firstFreeMinor(h uint32) uint32 {
	bucket := m.bucket(h)
	for minor := uint32(0); ; minor++ {
		if _, ok := bucket[minor]; !ok {
			return minor
		}
	}
}

func (m *modelState) currentKey(name string) modelKey {
	return m.items[name].key
}

func (m *modelState) add(name string, ino uint64) (modelKey, error) {
	if name == "" {
		return modelKey{}, ErrInvalidArgument
	}
	if _, ok := m.items[name]; ok {
		return modelKey{}, ErrExists
	}
	h := hashName(m.seed, name)
	if len(m.bucket(h)) >= m.limit {
		return modelKey{}, ErrHashFull
	}
	key := modelKey{h: h, minor: m.firstFreeMinor(h)}
	m.items[name] = modelItem{ino: ino, key: key}
	return key, nil
}

func (m *modelState) remove(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	if _, ok := m.items[name]; !ok {
		return ErrNotFound
	}
	delete(m.items, name)
	return nil
}

func (m *modelState) rename(oldName, newName string) (modelKey, error) {
	if oldName == "" || newName == "" {
		return modelKey{}, ErrInvalidArgument
	}
	oldItem, ok := m.items[oldName]
	if !ok {
		return modelKey{}, ErrNotFound
	}
	if newName == oldName {
		return modelKey{}, ErrExists
	}
	if _, ok := m.items[newName]; ok {
		return modelKey{}, ErrExists
	}
	oldHash := hashName(m.seed, oldName)
	newHash := hashName(m.seed, newName)
	count := len(m.bucket(newHash))
	if oldHash == newHash {
		count--
	}
	if count >= m.limit {
		return modelKey{}, ErrHashFull
	}
	delete(m.items, oldName)
	key := modelKey{h: newHash, minor: m.firstFreeMinor(newHash)}
	m.items[newName] = modelItem{ino: oldItem.ino, key: key}
	return key, nil
}

func (m *modelState) read(c Cookie, n int) ([]modelEntry, Cookie, bool, error) {
	if n < 0 {
		return nil, Cookie{}, false, ErrInvalidArgument
	}
	normalized, err := m.normalize(c)
	if err != nil {
		return nil, Cookie{}, false, err
	}
	entries := make([]modelEntry, 0)
	for name, item := range m.items {
		key := m.currentKey(name)
		if modelKeyUint64(key) >= uint64(normalized.Pos) {
			entries = append(entries, modelEntry{name: name, ino: item.ino, key: key})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return modelKeyUint64(entries[i].key) < modelKeyUint64(entries[j].key)
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	if len(entries) == 0 {
		return entries, normalized, !m.hasAtLeast(normalized.Pos), nil
	}
	next := Cookie{Gen: m.gen, Pos: Key(modelKeyUint64(entries[len(entries)-1].key) + 1)}
	return entries, next, !m.hasAtLeast(next.Pos), nil
}

func (m *modelState) cookieOf(name string) (Cookie, error) {
	if name == "" {
		return Cookie{}, ErrInvalidArgument
	}
	if _, ok := m.items[name]; !ok {
		return Cookie{}, ErrNotFound
	}
	return Cookie{Gen: m.gen, Pos: Key(modelKeyUint64(m.currentKey(name)) + 1)}, nil
}

func (m *modelState) rehash(newSeed uint32) error {
	names := make([]string, 0, len(m.items))
	for name := range m.items {
		names = append(names, name)
	}
	sort.Strings(names)
	counts := make(map[uint32]int)
	for _, name := range names {
		h := hashName(newSeed, name)
		counts[h]++
		if counts[h] > m.limit {
			return ErrHashFull
		}
	}
	snapshot := make(map[string]modelKey, len(names))
	for _, name := range names {
		snapshot[name] = m.currentKey(name)
	}
	m.prevGen = m.gen
	m.prev = snapshot
	m.seed = newSeed
	m.gen++
	used := make(map[uint32]uint32)
	for _, name := range names {
		h := hashName(newSeed, name)
		item := m.items[name]
		item.key = modelKey{h: h, minor: used[h]}
		m.items[name] = item
		used[h]++
	}
	return nil
}

func (m *modelState) normalize(c Cookie) (Cookie, error) {
	if c.Pos == 0 {
		return Cookie{Gen: m.gen}, nil
	}
	if c.Gen == m.gen {
		return c, nil
	}
	if c.Gen+1 == m.gen && m.prevGen == c.Gen {
		oldKey := uint64(c.Pos) - 1
		for name, key := range m.prev {
			if modelKeyUint64(key) == oldKey {
				if _, ok := m.items[name]; ok {
					return Cookie{Gen: m.gen, Pos: Key(modelKeyUint64(m.currentKey(name)) + 1)}, nil
				}
			}
		}
	}
	return Cookie{}, ErrCursorExpired
}

func (m *modelState) hasAtLeast(pos Key) bool {
	for name := range m.items {
		if modelKeyUint64(m.currentKey(name)) >= uint64(pos) {
			return true
		}
	}
	return false
}

func TestRandomModelComparison(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(strconv.Itoa(sequence), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(1102), uint64(sequence+1)))
			seed := uint32(rng.UintN(4))
			limit := int(1 + rng.UintN(4))
			d, err := NewHashDirectory(seed, limit)
			if err != nil {
				t.Fatal(err)
			}
			m := newModel(seed, limit)
			for step := 0; step < 30+rng.IntN(40); step++ {
				name := randomModelName(rng)
				other := randomModelName(rng)
				switch rng.IntN(9) {
				case 0:
					gotKey, gotErr := d.Add(name, uint64(step+1))
					wantKey, wantErr := m.add(name, uint64(step+1))
					m.logf("Add(%q,%d) => key=%d err=%v; model key=%d err=%v", name, step+1, gotKey, gotErr, modelKeyUint64(wantKey), wantErr)
					compareModelErr(t, m, gotErr, wantErr)
					compareModelKey(t, m, gotKey, wantKey, gotErr)
				case 1:
					gotErr := d.Remove(name)
					wantErr := m.remove(name)
					m.logf("Remove(%q) => err=%v; model err=%v", name, gotErr, wantErr)
					compareModelErr(t, m, gotErr, wantErr)
				case 2, 3:
					gotKey, gotErr := d.Rename(name, other)
					wantKey, wantErr := m.rename(name, other)
					m.logf("Rename(%q,%q) => key=%d err=%v; model key=%d err=%v", name, other, gotKey, gotErr, modelKeyUint64(wantKey), wantErr)
					compareModelErr(t, m, gotErr, wantErr)
					compareModelKey(t, m, gotKey, wantKey, gotErr)
				case 4, 5, 6:
					n := []int{0, 0, 1, 2, 5, 100}[rng.IntN(6)]
					c := randomModelCookie(rng, m)
					gotEntries, gotCookie, gotDone, gotErr := d.ReadDir(c, n)
					wantEntries, wantCookie, wantDone, wantErr := m.read(c, n)
					m.logf("ReadDir(%+v,%d) => entries=%s cookie=%+v done=%v err=%v; model entries=%s cookie=%+v done=%v err=%v",
						c, n, formatActualEntries(gotEntries), gotCookie, gotDone, gotErr,
						formatModelEntries(wantEntries), wantCookie, wantDone, wantErr)
					compareModelErr(t, m, gotErr, wantErr)
					if gotErr == nil {
						compareModelEntries(t, m, gotEntries, wantEntries)
						if gotCookie != wantCookie || gotDone != wantDone {
							t.Fatalf("%s\ncookie/done mismatch got=(%+v,%v) want=(%+v,%v)",
								strings.Join(m.log, "\n"), gotCookie, gotDone, wantCookie, wantDone)
						}
					}
				case 7:
					gotCookie, gotErr := d.CookieOf(name)
					wantCookie, wantErr := m.cookieOf(name)
					m.logf("CookieOf(%q) => cookie=%+v err=%v; model cookie=%+v err=%v", name, gotCookie, gotErr, wantCookie, wantErr)
					compareModelErr(t, m, gotErr, wantErr)
					if gotErr == nil && gotCookie != wantCookie {
						t.Fatalf("%s\ncookie mismatch got=%+v want=%+v", strings.Join(m.log, "\n"), gotCookie, wantCookie)
					}
				default:
					newSeed := uint32(rng.UintN(5))
					gotErr := d.Rehash(newSeed)
					wantErr := m.rehash(newSeed)
					m.logf("Rehash(%d) => err=%v; model err=%v", newSeed, gotErr, wantErr)
					compareModelErr(t, m, gotErr, wantErr)
				}
			}
			t.Logf("random sequence inputs, outputs and decisions:\n%s", strings.Join(m.log, "\n"))
		})
	}
}

func randomModelName(rng *rand.Rand) string {
	if rng.IntN(12) == 0 {
		return ""
	}
	if rng.IntN(3) == 0 {
		return collisionNames[rng.IntN(len(collisionNames))]
	}
	bytes := []byte{
		byte('a' + rng.IntN(7)),
		byte('a' + rng.IntN(7)),
		byte('0' + rng.IntN(4)),
	}
	return string(bytes)
}

func randomModelCookie(rng *rand.Rand, m *modelState) Cookie {
	switch rng.IntN(5) {
	case 0:
		return Cookie{}
	case 1:
		return Cookie{Gen: m.gen}
	case 2:
		return Cookie{Gen: m.gen, Pos: Key(1 + rng.Uint64N(4))}
	case 3:
		if m.gen > 1 {
			return Cookie{Gen: m.gen - 1, Pos: Key(1 + rng.Uint64N(4))}
		}
	}
	return Cookie{Gen: 1 + uint64(rng.IntN(int(m.gen)+2)), Pos: Key(1 + rng.Uint64N(5))}
}

func compareModelErr(t *testing.T, m *modelState, got, want error) {
	t.Helper()
	sentinels := []error{ErrInvalidArgument, ErrNotFound, ErrExists, ErrHashFull, ErrCursorExpired}
	for _, sentinel := range sentinels {
		if errors.Is(got, sentinel) || errors.Is(want, sentinel) {
			if errors.Is(got, sentinel) && errors.Is(want, sentinel) {
				return
			}
			t.Fatalf("%s\nerror mismatch got=%v want=%v", strings.Join(m.log, "\n"), got, want)
		}
	}
	if got != nil || want != nil {
		t.Fatalf("%s\nerror mismatch got=%v want=%v", strings.Join(m.log, "\n"), got, want)
	}
}

func compareModelKey(t *testing.T, m *modelState, got Key, want modelKey, err error) {
	t.Helper()
	if err == nil && uint64(got) != modelKeyUint64(want) {
		t.Fatalf("%s\nkey mismatch got=%d want=%d", strings.Join(m.log, "\n"), got, modelKeyUint64(want))
	}
}

func compareModelEntries(t *testing.T, m *modelState, got []DirEntry, want []modelEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s\nentries length got=%d want=%d", strings.Join(m.log, "\n"), len(got), len(want))
	}
	for i := range got {
		if got[i].Name != want[i].name || got[i].Ino != want[i].ino || uint64(got[i].Key) != modelKeyUint64(want[i].key) {
			t.Fatalf("%s\nentry %d got={key=%d name=%q ino=%d} want={key=%d name=%q ino=%d}",
				strings.Join(m.log, "\n"), i, got[i].Key, got[i].Name, got[i].Ino,
				modelKeyUint64(want[i].key), want[i].name, want[i].ino)
		}
	}
}

func formatActualEntries(entries []DirEntry) string {
	parts := make([]string, len(entries))
	for i, entry := range entries {
		parts[i] = fmt.Sprintf("{key=%d name=%q ino=%d}", entry.Key, entry.Name, entry.Ino)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatModelEntries(entries []modelEntry) string {
	parts := make([]string, len(entries))
	for i, entry := range entries {
		parts[i] = fmt.Sprintf("{key=%d name=%q ino=%d}", modelKeyUint64(entry.key), entry.name, entry.ino)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
