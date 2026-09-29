package pkchange

import (
	"reflect"
	"testing"
)

func TestMergeKeepsLastPerKey(t *testing.T) {
	events := []Event{
		{Kind: EventWrite, Key: "a", Data: r("v", "1")},
		{Kind: EventDelete, Key: "b"},
		{Kind: EventWrite, Key: "a", Data: r("v", "2")},
		{Kind: EventWrite, Key: "c", Data: r("v", "3")},
		{Kind: EventDelete, Key: "a"},
	}

	got := Merge(events)
	want := []Event{
		{Kind: EventDelete, Key: "b"},
		{Kind: EventWrite, Key: "c", Data: r("v", "3")},
		{Kind: EventDelete, Key: "a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %#v, want %#v", got, want)
	}

	// 原序列不被修改。
	if events[0].Data["v"] != "1" {
		t.Fatalf("input events mutated")
	}
}

func TestMergeDeterministic(t *testing.T) {
	events := []Event{
		{Kind: EventWrite, Key: "x"},
		{Kind: EventWrite, Key: "y"},
		{Kind: EventDelete, Key: "x"},
	}
	first := Merge(events)
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(Merge(events), first) {
			t.Fatalf("Merge not deterministic on repeat %d", i)
		}
	}
}

func TestPartitionByKey(t *testing.T) {
	events := Merge([]Event{
		{Kind: EventWrite, Key: "a"},
		{Kind: EventWrite, Key: "b"},
		{Kind: EventWrite, Key: "c"},
		{Kind: EventWrite, Key: "d"},
	})

	const n = 4
	parts := PartitionByKey(events, n)
	if len(parts) != n {
		t.Fatalf("got %d partitions, want %d", len(parts), n)
	}

	// 每个键落在 KeyPartition 指定的分区，且每分区内顺序与全局顺序一致。
	seen := 0
	for _, p := range parts {
		if p.Index < 0 || p.Index >= n {
			t.Fatalf("bad partition index %d", p.Index)
		}
		for _, ev := range p.Events {
			if KeyPartition(ev.Key, n) != p.Index {
				t.Fatalf("key %q in wrong partition %d", ev.Key, p.Index)
			}
			seen++
		}
	}
	if seen != len(events) {
		t.Fatalf("partitioned %d events, want %d", seen, len(events))
	}

	// 重复计算结果完全相同。
	again := PartitionByKey(events, n)
	if !reflect.DeepEqual(parts, again) {
		t.Fatalf("partitioning not deterministic")
	}
}

func TestPartitionPanicsOnBadCount(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on non-positive partition count")
		}
	}()
	PartitionByKey(nil, 0)
}
