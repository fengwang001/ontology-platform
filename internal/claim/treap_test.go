package claim

import (
	"sort"
	"testing"
)

type brutePolicy struct {
	id    string
	start int
	end   int
}

func bruteCover(list []brutePolicy, day int) []string {
	var got []string
	for _, p := range list {
		if p.start <= day && day <= p.end {
			got = append(got, p.id)
		}
	}
	sort.Strings(got)
	return got
}

func TestTreapMatchesBruteForce(t *testing.T) {
	// Deterministic pseudo-random mix so failures are reproducible.
	var seed uint64 = 0x123456789abcdef
	next := func() uint64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}

	tr := newTreap()
	var list []brutePolicy
	ids := map[string]bool{}

	for step := 0; step < 4000; step++ {
		if len(list) == 0 || next()%3 != 0 {
			id := ""
			for {
				id = string(rune('a'+next()%26)) + string(rune('a'+next()%26)) +
					string(rune('0'+next()%10))
				if !ids[id] {
					ids[id] = true
					break
				}
			}
			start := int(next() % 20)
			end := start + int(next()%20)
			tr.insert(id, start, end)
			list = append(list, brutePolicy{id, start, end})
		} else {
			idx := next() % uint64(len(list))
			p := list[idx]
			list = append(list[:idx], list[idx+1:]...)
			delete(ids, p.id)
			tr.erase(p.start, p.id)
		}

		for day := -2; day <= 42; day += 3 {
			got := tr.coveringAt(day, nil)
			sort.Strings(got)
			want := bruteCover(list, day)
			if stringsJoin(got) != stringsJoin(want) {
				t.Fatalf("step %d day %d: got %v want %v", step, day, got, want)
			}
		}
	}
}

func stringsJoin(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
