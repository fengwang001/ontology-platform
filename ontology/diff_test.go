package ontology

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func entries(keys ...string) []Entry {
	out := make([]Entry, len(keys))
	for i, k := range keys {
		out[i] = Entry{Key: k, Value: "v-" + k}
	}
	return out
}

// naiveDiff 是不依赖归并实现的朴素参照：基于 map 全量比对后按键排序。
func naiveDiff(t *testing.T, oldSnap, newSnap []Entry) []Change {
	t.Helper()
	oldMap := map[string]string{}
	newMap := map[string]string{}
	keys := map[string]bool{}
	for _, e := range oldSnap {
		oldMap[e.Key] = e.Value
		keys[e.Key] = true
	}
	for _, e := range newSnap {
		newMap[e.Key] = e.Value
		keys[e.Key] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sortStrings(sorted)
	changes := []Change{}
	for _, k := range sorted {
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

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func equalEntries(a, b []Entry) bool {
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

func TestMergeDiffCases(t *testing.T) {
	old := []Entry{
		{Key: "a", Value: "1"},
		{Key: "b", Value: "2"},
		{Key: "c", Value: "3"},
		{Key: "e", Value: "5"},
	}
	new := []Entry{
		{Key: "b", Value: "2"}, // 值相同，不输出
		{Key: "c", Value: "33"},
		{Key: "d", Value: "4"},
		{Key: "e", Value: "5"}, // 值相同，不输出
		{Key: "f", Value: "6"},
		{Key: "g", Value: "7"},
	}
	want := []Change{
		{Op: OpDelete, Key: "a", OldValue: "1"},
		{Op: OpUpdate, Key: "c", OldValue: "3", NewValue: "33"},
		{Op: OpInsert, Key: "d", NewValue: "4"},
		{Op: OpInsert, Key: "f", NewValue: "6"},
		{Op: OpInsert, Key: "g", NewValue: "7"},
	}
	got, err := MergeDiff(context.Background(), old, new, 0, nil)
	if err != nil {
		t.Fatalf("MergeDiff: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes mismatch:\n got=%v\nwant=%v", got, want)
	}
	if ref := naiveDiff(t, old, new); !reflect.DeepEqual(got, ref) {
		t.Fatalf("diff differs from naive reference:\n got=%v\n ref=%v", got, ref)
	}

	// 变更日志必须按键严格升序且每键至多一条。
	seen := map[string]bool{}
	prev := ""
	for _, c := range got {
		if c.Key <= prev {
			t.Fatalf("changes not strictly sorted at key %q", c.Key)
		}
		if seen[c.Key] {
			t.Fatalf("key %q appears more than once", c.Key)
		}
		seen[c.Key] = true
		prev = c.Key
	}
}

func TestMergeDiffTailRemainders(t *testing.T) {
	// 旧侧尾部剩余：逐个 delete
	old := entries("a", "b", "c")
	got, err := MergeDiff(context.Background(), old, nil, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Op: OpDelete, Key: "a", OldValue: "v-a"},
		{Op: OpDelete, Key: "b", OldValue: "v-b"},
		{Op: OpDelete, Key: "c", OldValue: "v-c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("old tail:\n got=%v\nwant=%v", got, want)
	}

	// 新侧尾部剩余：逐个 insert
	got, err = MergeDiff(context.Background(), nil, entries("x", "y"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []Change{
		{Op: OpInsert, Key: "x", NewValue: "v-x"},
		{Op: OpInsert, Key: "y", NewValue: "v-y"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("new tail:\n got=%v\nwant=%v", got, want)
	}

	// 值全部相同：零变更
	same := []Entry{{Key: "k", Value: "same"}}
	got, err = MergeDiff(context.Background(), same, []Entry{{Key: "k", Value: "same"}}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("identical snapshots must produce no changes, got %v", got)
	}
}

func TestMergeDiffAgainstNaiveReference(t *testing.T) {
	cases := [][2][]Entry{
		{nil, nil},
		{nil, entries("a")},
		{entries("a"), nil},
		{entries("a", "b", "c"), entries("a", "b", "c")},
		{entries("a", "c", "e"), entries("b", "c", "d", "e", "f")},
		{entries("m", "n"), entries("a", "b", "z")},
	}
	for i, pair := range cases {
		old, next := pair[0], pair[1]
		// 值在参照实现里保持同键同值，额外构造一个值变化用例
		got, err := MergeDiff(context.Background(), old, next, 0, nil)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if want := naiveDiff(t, old, next); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d mismatch:\n got=%v\nwant=%v", i, got, want)
		}
		// 重放等价
		replayed, err := Replay(old, got)
		if err != nil {
			t.Fatalf("case %d replay: %v", i, err)
		}
		if !equalEntries(replayed, next) {
			t.Fatalf("case %d replay != new snapshot:\n got=%v\nwant=%v", i, replayed, next)
		}
	}
}

func TestMergeDiffValueChange(t *testing.T) {
	old := []Entry{{Key: "k", Value: "old"}, {Key: "z", Value: "1"}}
	next := []Entry{{Key: "k", Value: "new"}, {Key: "z", Value: "1"}}
	got, err := MergeDiff(context.Background(), old, next, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{Op: OpUpdate, Key: "k", OldValue: "old", NewValue: "new"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got=%v\nwant=%v", got, want)
	}
	replayed, err := Replay(old, got)
	if err != nil {
		t.Fatal(err)
	}
	if !equalEntries(replayed, next) {
		t.Fatalf("replay mismatch: %v", replayed)
	}
}

func TestMergeDiffTooManyChanges(t *testing.T) {
	old := entries("a", "b")
	next := entries("c", "d") // 4 条变更
	if _, err := MergeDiff(context.Background(), old, next, 4, nil); err != nil {
		t.Fatalf("limit equal to count must pass, got %v", err)
	}
	_, err := MergeDiff(context.Background(), old, next, 3, nil)
	if !errors.Is(err, ErrTooManyChanges) {
		t.Fatalf("want ErrTooManyChanges, got %v", err)
	}
	_, err = MergeDiff(context.Background(), old, next, -1, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig for negative limit, got %v", err)
	}
}

func TestMergeDiffLogsEveryStep(t *testing.T) {
	var sb strings.Builder
	log := funcLogger(func(_ context.Context, format string, args ...any) {
		fmt.Fprintf(&sb, format+"\n", args...)
	})
	old := []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	next := []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "9"}, {Key: "c", Value: "3"}}
	if _, err := MergeDiff(context.Background(), old, next, 0, log); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		"none key=\"a\" reason=identical",
		"update key=\"b\" reason=key in both, value differs",
		"insert key=\"c\"",
		"merge done changes=2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, out)
		}
	}
}

type funcLogger func(ctx context.Context, format string, args ...any)

func (f funcLogger) Logf(ctx context.Context, format string, args ...any) { f(ctx, format, args...) }

func TestReplayRejectsMalformedLog(t *testing.T) {
	base := entries("a")
	if _, err := Replay(base, []Change{{Op: OpDelete, Key: "a", OldValue: "WRONG"}}); err == nil {
		t.Fatal("delete with mismatched old value must fail")
	}
	if _, err := Replay(base, []Change{{Op: OpInsert, Key: "a", NewValue: "x"}}); err == nil {
		t.Fatal("insert of existing key must fail")
	}
	if _, err := Replay(base, []Change{
		{Op: OpInsert, Key: "b", NewValue: "x"},
		{Op: OpInsert, Key: "a", NewValue: "y"},
	}); err == nil {
		t.Fatal("unsorted change log must fail")
	}
}
