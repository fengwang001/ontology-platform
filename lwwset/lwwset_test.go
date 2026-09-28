package lwwset

import (
	"errors"
	"testing"
)

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		name  string
		id    int64
		limit int
		want  error
	}{
		{"zero id", 0, 10, ErrInvalidReplicaID},
		{"negative id", -3, 10, ErrInvalidReplicaID},
		{"zero limit", 1, 0, ErrInvalidLimit},
		{"negative limit", 1, -1, ErrInvalidLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewReplica(c.id, c.limit); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestTieGoesToRemove(t *testing.T) {
	r, _ := NewReplica(1, 100)

	must(t, r.Add("x", 5))
	dump(t, "add x@5 -> member", r)
	if !r.Contains("x") {
		t.Fatal("x should be present after add@5")
	}

	// 并列时间戳：删除与添加相等，稳定偏向删除。
	must(t, r.Remove("x", 5))
	dump(t, "remove x@5 (tie) -> removed", r)
	if r.Contains("x") {
		t.Fatal("x must be absent when addTime == removeTime (tie favors remove)")
	}

	// 同样并列时间戳的重新添加不能翻案：删除严格偏置。
	must(t, r.Add("x", 5))
	dump(t, "re-add x@5 (still tie) -> removed", r)
	if r.Contains("x") {
		t.Fatal("equal-timestamp add must not override the tombstone")
	}

	// 严格更新的添加时间才能复活元素。
	must(t, r.Add("x", 6))
	dump(t, "add x@6 -> member", r)
	if !r.Contains("x") {
		t.Fatal("x should be present after strictly newer add@6")
	}

	rec, _ := r.Lookup("x")
	if rec.AddTime != 6 || rec.RemoveTime != 5 {
		t.Fatalf("unexpected record: %+v", rec)
	}
}

func TestOutOfOrderTimestampsDoNotShrink(t *testing.T) {
	r, _ := NewReplica(1, 100)

	must(t, r.Add("a", 10))
	must(t, r.Remove("a", 12))
	must(t, r.Add("a", 8))    // 更旧的添加晚到
	must(t, r.Remove("a", 3)) // 更旧的删除晚到
	dump(t, "stale add/remove arrive late -> a stays {10,12} removed", r)

	rec, ok := r.Lookup("a")
	if !ok || rec.AddTime != 10 || rec.RemoveTime != 12 {
		t.Fatalf("records shrank: %+v ok=%v", rec, ok)
	}
	if r.Contains("a") {
		t.Fatal("a must remain removed")
	}

	// 先删后加，且添加时间更旧：墓碑不因晚到的旧添加而消失。
	must(t, r.Remove("b", 20))
	if r.Contains("b") {
		t.Fatal("pure remove before any add must not make b present")
	}
	must(t, r.Add("b", 19))
	dump(t, "remove b@20 then late add b@19 -> absent", r)
	if r.Contains("b") {
		t.Fatal("late older add must not resurrect b")
	}
	must(t, r.Add("b", 21))
	if !r.Contains("b") {
		t.Fatal("b should return with add@21")
	}
	dump(t, "add b@21 -> member", r)

	// 每个被接受的操作都追加一条变更。
	if got := r.Seq(); got != 7 {
		t.Fatalf("seq want 7, got %d", got)
	}
}

func TestInvalidOpsLeaveNoTrace(t *testing.T) {
	r, _ := NewReplica(1, 2)
	must(t, r.Add("a", 1))
	must(t, r.Add("b", 1))

	seqBefore := r.Seq()
	sumBefore := r.Checksum()

	try := func(name string, fn func() error, want error) {
		t.Helper()
		err := fn()
		if !errors.Is(err, want) {
			t.Fatalf("%s: want %v, got %v", name, want, err)
		}
		if r.Seq() != seqBefore {
			t.Fatalf("%s: seq changed after rejection %d -> %d", name, seqBefore, r.Seq())
		}
		if r.Checksum() != sumBefore {
			t.Fatalf("%s: records changed after rejection", name)
		}
		t.Logf("[reject] %s -> %v; seq stays %d, checksum unchanged", name, err, seqBefore)
	}

	try("add empty", func() error { return r.Add("", 5) }, ErrEmptyElement)
	try("remove empty", func() error { return r.Remove("", 5) }, ErrEmptyElement)
	try("add ts zero", func() error { return r.Add("c", 0) }, ErrNonPositiveTimestamp)
	try("remove ts negative", func() error { return r.Remove("c", -9) }, ErrNonPositiveTimestamp)
	try("both invalid favors empty", func() error { return r.Add("", 0) }, ErrEmptyElement)
	try("over limit add", func() error { return r.Add("c", 5) }, ErrLimitExceeded)
	try("over limit remove", func() error { return r.Remove("c", 5) }, ErrLimitExceeded)

	// 已存在元素不额外占名额，并列时间戳操作仍被接受（且仍偏删除）。
	must(t, r.Remove("a", 1))
	must(t, r.Add("a", 1))
	dump(t, "after rejected over-limit ops + same-element tie ops", r)
	if r.Contains("a") {
		t.Fatal("tie still favors remove for existing element a")
	}
}
