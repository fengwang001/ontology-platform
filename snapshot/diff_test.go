package snapshot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
)

func entries(kv ...string) []Entry {
	out := make([]Entry, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, Entry{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func changeKeys(changes []Change) []string {
	out := make([]string, 0, len(changes))
	for _, ch := range changes {
		out = append(out, ch.Key)
	}
	return out
}

// naiveDiff 是朴素参照实现：不依赖有序前提，用 map 集合运算后按键排序输出。
func naiveDiff(oldSnap, newSnap []Entry) []Change {
	oldMap := map[string]string{}
	newMap := map[string]string{}
	for _, e := range oldSnap {
		oldMap[e.Key] = e.Value
	}
	for _, e := range newSnap {
		newMap[e.Key] = e.Value
	}
	all := map[string]struct{}{}
	for k := range oldMap {
		all[k] = struct{}{}
	}
	for k := range newMap {
		all[k] = struct{}{}
	}
	ordered := make([]string, 0, len(all))
	for k := range all {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var changes []Change
	for _, k := range ordered {
		ov, inOld := oldMap[k]
		nv, inNew := newMap[k]
		switch {
		case inOld && !inNew:
			changes = append(changes, Change{Op: OpDelete, Key: k, OldValue: ov})
		case !inOld && inNew:
			changes = append(changes, Change{Op: OpInsert, Key: k, NewValue: nv})
		case ov != nv:
			changes = append(changes, Change{Op: OpUpdate, Key: k, OldValue: ov, NewValue: nv})
		}
	}
	return changes
}

func assertReject(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection matching %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error matching %v, got %v", target, err)
	}
	var de *DiffError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DiffError, got %T: %v", err, err)
	}
}

func TestDiffMixedWithTailRemainder(t *testing.T) {
	oldSnap := entries("a", "1", "b", "2", "c", "3", "d", "4")
	newSnap := entries("b", "2", "c", "30", "e", "5", "f", "6")

	changes, err := Diff(context.Background(), Config{}, oldSnap, newSnap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []Change{
		{Op: OpDelete, Key: "a", OldValue: "1"},
		{Op: OpUpdate, Key: "c", OldValue: "3", NewValue: "30"},
		{Op: OpDelete, Key: "d", OldValue: "4"},
		{Op: OpInsert, Key: "e", NewValue: "5"},
		{Op: OpInsert, Key: "f", NewValue: "6"},
	}
	if fmt.Sprint(changes) != fmt.Sprint(want) {
		t.Fatalf("changes mismatch:\n got %v\nwant %v", changes, want)
	}
	if got := changeKeys(changes); fmt.Sprint(got) != "[a c d e f]" {
		t.Fatalf("unexpected keys/order: %v", got)
	}
	if got := Replay(oldSnap, changes); fmt.Sprint(got) != fmt.Sprint(newSnap) {
		t.Fatalf("replay mismatch:\n got %v\nwant %v", got, newSnap)
	}
	if ref := naiveDiff(oldSnap, newSnap); fmt.Sprint(changes) != fmt.Sprint(ref) {
		t.Fatalf("naive reference mismatch:\n got %v\nwant %v", changes, ref)
	}
}

func TestDiffEqualValuesNoOutput(t *testing.T) {
	snap := entries("a", "1", "b", "2", "c", "3")
	changes, err := Diff(context.Background(), Config{}, snap, entries("a", "1", "b", "2", "c", "3"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes, got %v", changes)
	}
	if got := Replay(snap, changes); fmt.Sprint(got) != fmt.Sprint(snap) {
		t.Fatalf("replay altered equal snapshot: %v", got)
	}
}

func TestDiffNewTailInserts(t *testing.T) {
	oldSnap := entries("a", "1")
	newSnap := entries("a", "1", "x", "2", "y", "3", "z", "4")
	changes, err := Diff(context.Background(), Config{}, oldSnap, newSnap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 3 || changes[0].Op != OpInsert || changes[2].Key != "z" {
		t.Fatalf("expected 3 ordered inserts, got %v", changes)
	}
	if got := Replay(oldSnap, changes); fmt.Sprint(got) != fmt.Sprint(newSnap) {
		t.Fatalf("replay mismatch: %v", got)
	}
}

func TestDiffOldTailDeletes(t *testing.T) {
	oldSnap := entries("a", "1", "x", "2", "y", "3", "z", "4")
	newSnap := entries("a", "1")
	changes, err := Diff(context.Background(), Config{}, oldSnap, newSnap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 3 || changes[0].Op != OpDelete || changes[2].Key != "z" {
		t.Fatalf("expected 3 ordered deletes, got %v", changes)
	}
	if got := Replay(oldSnap, changes); len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("replay should leave only a, got %v", got)
	}
}
