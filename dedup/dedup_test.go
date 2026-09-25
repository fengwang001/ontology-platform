package dedup

import (
	"reflect"
	"testing"
)

// TestSeenAddSnapshot 钉住基本语义：Seen/Add 与升序快照。
func TestSeenAddSnapshot(t *testing.T) {
	s := New()
	for _, txid := range []int64{5, 1, 9, 3} {
		if s.Seen(txid) {
			t.Fatalf("txid %d seen before add", txid)
		}
		s.Add(txid)
		if !s.Seen(txid) {
			t.Fatalf("txid %d not seen after add", txid)
		}
	}
	if got := s.Snapshot(); !reflect.DeepEqual(got, []int64{1, 3, 5, 9}) {
		t.Fatalf("snapshot not sorted/complete: %v", got)
	}
	// 精确集合：重复 Add 仍是同一集合。
	s.Add(1)
	if got := s.Snapshot(); !reflect.DeepEqual(got, []int64{1, 3, 5, 9}) {
		t.Fatalf("duplicate add changed set: %v", got)
	}
}

// TestProbeComplexity 证明查重按 txid 哈希直接定位：
// 集合大小 m 跨多档，全新 txid 的探查个数恒为与 m 无关的小常数。
func TestProbeComplexity(t *testing.T) {
	const maxProbes = 3
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := int64(1); i <= int64(m); i++ {
			s.Add(i)
		}
		if s.Seen(int64(m) + 1) {
			t.Fatalf("fresh txid reported seen at m=%d", m)
		}
		if s.probes > maxProbes {
			t.Fatalf("m=%d: probes=%d > %d, lookup not O(1)", m, s.probes, maxProbes)
		}
		if first == -1 {
			first = s.probes
		} else if s.probes != first {
			t.Fatalf("probes grew with m: m=100 -> %d, m=%d -> %d", first, m, s.probes)
		}
	}
}
