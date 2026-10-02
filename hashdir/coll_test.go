package hashdir

import (
	"os"
	"sort"
	"strconv"
	"testing"
)

// collSeed is the seed used by the brute-forced collision groups below.
const collSeed = uint32(7)

// collNames holds names that all share one hash under collSeed (sorted
// byte-wise); pairNames holds two more names sharing a different hash.
var (
	collNames []string
	pairNames []string
)

func TestMain(m *testing.M) {
	coll, pair := findCollisionGroups(collSeed, 3)
	collNames = coll
	pairNames = pair
	os.Exit(m.Run())
}

// findCollisionGroups brute-forces short names until it finds one group of
// k names sharing a hash and one group of 2 names sharing a different hash
// under seed. Names are generated as "n0", "n1", ... deterministically.
func findCollisionGroups(seed uint32, k int) (groupK []string, group2 []string) {
	type pair struct {
		h uint32
		i int
	}
	var all []pair
	const batch = 1 << 22
	for start := 0; ; start += batch {
		for i := start; i < start+batch; i++ {
			all = append(all, pair{h: hashName(seed, "n"+strconv.Itoa(i)), i: i})
		}
		sort.Slice(all, func(a, b int) bool {
			if all[a].h != all[b].h {
				return all[a].h < all[b].h
			}
			return all[a].i < all[b].i
		})
		for i := 0; i < len(all); {
			j := i
			for j < len(all) && all[j].h == all[i].h {
				j++
			}
			run := j - i
			if groupK == nil && run >= k {
				for _, p := range all[i : i+k] {
					groupK = append(groupK, "n"+strconv.Itoa(p.i))
				}
				sort.Strings(groupK)
			}
			if group2 == nil && run >= 2 && (groupK == nil || all[i].h != hashName(seed, groupK[0])) {
				for _, p := range all[i : i+2] {
					group2 = append(group2, "n"+strconv.Itoa(p.i))
				}
				sort.Strings(group2)
			}
			i = j
		}
		if groupK != nil && group2 != nil {
			return groupK, group2
		}
	}
}

// minorOf extracts the minor (low 32 bits) of a key.
func minorOf(key uint64) uint32 { return uint32(key) }

// hashOf extracts the hash (high 32 bits) of a key.
func hashOf(key uint64) uint32 { return uint32(key >> 32) }

func mustAdd(t *testing.T, s *Store, name string, ino uint64) uint64 {
	t.Helper()
	key, err := s.Add(name, ino)
	if err != nil {
		t.Fatalf("Add(%q): %v", name, err)
	}
	return key
}

func mustStore(t *testing.T, m uint32) *Store {
	t.Helper()
	s, err := New(collSeed, m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}
