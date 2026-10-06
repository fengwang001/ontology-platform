package scrub_test

import (
	"sort"

	"ontology/scrub"
	"ontology/scrub/naivemodel"
)

func sortedFail(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out
}

func sameIntSet(a, b []int) bool {
	x := append([]int(nil), a...)
	y := append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sameSeq(a, b []int) bool {
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

func targetSummary(ts []scrub.RepairTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		kind := "stale"
		if t.Kind == scrub.RepairBitrot {
			kind = "bitrot"
		}
		out = append(out, kind+":node"+itoa(t.Node))
	}
	sort.Strings(out)
	return out
}

func naiveTargetSummary(ts []naivemodel.Target) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		kind := "stale"
		if t.Bitrot {
			kind = "bitrot"
		}
		out = append(out, kind+":node"+itoa(t.Node))
	}
	sort.Strings(out)
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}

func replicaSnapshot(s *scrub.Store, id int) ([]scrub.Replica, bool) {
	return s.ReplicasForTest(id)
}

func replicasEqual(a []scrub.Replica, b []naivemodel.Replica) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Node != b[i].Node ||
			a[i].Version != b[i].Version ||
			a[i].SavedDigest != b[i].SavedDigest ||
			a[i].ActualDigest != b[i].ActualDigest {
			return false
		}
	}
	return true
}
