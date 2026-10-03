package nseccache_test

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/nseccache"
)

var labelPool = []string{"a", "b", "c", "d", "e", "z", "aa", "ab", "b1", "x-y", "p_q"}

func randomZoneName(rng *rand.Rand, apex []string, allowWild bool) string {
	n := rng.Intn(4)
	parts := make([]string, 0, n+len(apex)+1)
	if allowWild && rng.Intn(6) == 0 {
		parts = append(parts, "*")
	}
	for i := 0; i < n; i++ {
		parts = append(parts, labelPool[rng.Intn(len(labelPool))])
	}
	parts = append(parts, apex...)
	return labelsKey(parts)
}

func randomOutOfZone(rng *rand.Rand) string {
	n := rng.Intn(3) + 1
	parts := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		parts = append(parts, labelPool[rng.Intn(len(labelPool))])
	}
	parts = append(parts, "other")
	return labelsKey(parts)
}

func randomTypes(rng *rand.Rand) map[uint16]bool {
	types := map[uint16]bool{47: true}
	switch rng.Intn(6) {
	case 0:
		types[6] = true
		types[2] = true
	case 1:
		types[1] = true
	case 2:
		types[5] = true
	case 3:
		types[15] = true
	}
	return types
}

// chainRecords 构造区间两两不重叠、至多一条回绕的 NSEC 链记录。
func chainRecords(rng *rand.Rand, apex []string) []nseccache.Record {
	nnode := rng.Intn(7) + 2
	seen := make(map[string]bool)
	var nodes [][]string
	for i := 0; i < nnode; i++ {
		depth := rng.Intn(3) + 1
		parts := make([]string, 0, depth+len(apex))
		for j := 0; j < depth; j++ {
			parts = append(parts, labelPool[rng.Intn(len(labelPool))])
		}
		parts = append(parts, apex...)
		key := labelsKey(parts)
		if seen[key] {
			continue
		}
		seen[key] = true
		nl, _ := parseLabels(key)
		nodes = append(nodes, nl)
	}
	sort.Slice(nodes, func(i, j int) bool { return cmpLabels(nodes[i], nodes[j]) < 0 })
	ttls := []int{0, 50, 100, 200, 300, 86400}
	var recs []nseccache.Record
	for i, owner := range nodes {
		next := nodes[(i+1)%len(nodes)]
		recs = append(recs, nseccache.Record{
			Owner: labelsKey(owner), Next: labelsKey(next),
			Types: randomTypes(rng), TTL: ttls[rng.Intn(len(ttls))],
			Validated: true,
		})
	}
	return recs
}

func kindString(k nseccache.ResultKind) string {
	switch k {
	case nseccache.ResultNXDomain:
		return "NXDomain"
	case nseccache.ResultNoData:
		return "NoData"
	default:
		return "Miss"
	}
}

func sameResult(a, b nseccache.Result) bool {
	if a.Kind != b.Kind || a.TTL != b.TTL || len(a.Used) != len(b.Used) {
		return false
	}
	for i := range a.Used {
		al, _ := parseLabels(a.Used[i].Owner)
		bl, _ := parseLabels(b.Used[i].Owner)
		if cmpLabels(al, bl) != 0 {
			return false
		}
	}
	return true
}

func TestRandomDifferential(t *testing.T) {
	const trials = 2000
	const opsPerTrial = 40
	rng := rand.New(rand.NewSource(20261003))
	zones := []string{"example", "com", "org", "test"}

	for trial := 0; trial < trials; trial++ {
		apex, _ := parseLabels(zones[rng.Intn(len(zones))])
		soaMin := []int{0, 50, 100, 200, 86400}[rng.Intn(5)]
		capN := rng.Intn(12) + 1
		cache, err := nseccache.New(labelsKey(apex), soaMin, capN)
		if err != nil {
			t.Fatalf("trial %d: New: %v", trial, err)
		}
		model := newNaive(labelsKey(apex), soaMin, capN)

		now := rng.Intn(1000)

		t.Logf("=== trial %d zone=%s soaMin=%d N=%d ===", trial, labelsKey(apex), soaMin, capN)

		for op := 0; op < opsPerTrial; op++ {
			// 时间通常单调，偶尔回退以触发 ErrClockRewind
			switch rng.Intn(12) {
			case 0:
				now -= rng.Intn(50)
				if now < 0 {
					now = 0
				}
			default:
				now += rng.Intn(60)
			}

			if rng.Intn(2) == 0 {
				var rec nseccache.Record
				switch rng.Intn(10) {
				case 0:
					// 随机单点记录（可能造成区间重叠，用于压力测试）
					rec = nseccache.Record{
						Owner: randomZoneName(rng, apex, true),
						Next:  randomZoneName(rng, apex, true),
						Types: randomTypes(rng), TTL: rng.Intn(400),
						Validated: true,
					}
				case 1:
					rec = nseccache.Record{
						Owner: randomOutOfZone(rng),
						Next:  randomZoneName(rng, apex, false),
						Types: randomTypes(rng), TTL: 10, Validated: true,
					}
				case 2:
					rec = nseccache.Record{
						Owner: randomZoneName(rng, apex, false),
						Next:  randomZoneName(rng, apex, false),
						Types: map[uint16]bool{1: true}, TTL: 10, Validated: true,
					} // 缺 47
				case 3:
					rec = nseccache.Record{
						Owner: randomZoneName(rng, apex, false),
						Next:  randomZoneName(rng, apex, false),
						Types: randomTypes(rng), TTL: 10, Validated: false,
					}
				default:
					chain := chainRecords(rng, apex)
					rec = chain[rng.Intn(len(chain))]
				}
				got := cache.Insert(now, rec)
				want := model.insert(now, rec)
				t.Logf("op%d Insert now=%d owner=%s next=%s ttl=%d types=%v validated=%v -> got=%v want=%v",
					op, now, rec.Owner, rec.Next, rec.TTL, typeCodes(rec.Types), rec.Validated, got, want)
				if !errors.Is(got, want) {
					t.Fatalf("trial %d op %d Insert mismatch: got=%v want=%v", trial, op, got, want)
				}
			} else {
				q := randomZoneName(rng, apex, true)
				if rng.Intn(10) == 0 {
					q = randomOutOfZone(rng)
				}
				qt := []uint16{1, 2, 5, 6, 15, 47}[rng.Intn(6)]
				got, gerr := cache.Lookup(now, q, qt)
				want, werr := model.lookup(now, q, qt)
				t.Logf("op%d Lookup now=%d qname=%s qtype=%d -> got=(%s used=%v ttl=%d err=%v) want=(%s used=%v ttl=%d err=%v)",
					op, now, q, qt,
					kindString(got.Kind), ownerList(got.Used), got.TTL, gerr,
					kindString(want.Kind), ownerList(want.Used), want.TTL, werr)
				if !errors.Is(gerr, werr) || !sameResult(got, want) {
					t.Fatalf("trial %d op %d Lookup mismatch:\n got=%+v err=%v\nwant=%+v err=%v",
						trial, op, got, gerr, want, werr)
				}
			}
		}
	}
}

func typeCodes(types map[uint16]bool) []uint16 {
	var out []uint16
	for t := range types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func ownerList(recs []nseccache.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Owner
	}
	return out
}
