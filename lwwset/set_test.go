package lwwset

import (
	"errors"
	"reflect"
	"testing"
)

func logStep(t *testing.T, s *Set, step, basis string) {
	t.Helper()
	t.Logf("步骤=%s 副本=%s 记录=%v 判定依据=%s", step, s.ID(), s.Snapshot(), basis)
}

func TestTiePrefersRemove(t *testing.T) {
	s, err := New("r1", 16)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Add("a", 5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	logStep(t, s, "Add(a,5)", "无删除记录，5>0 存活")
	if !s.Contains("a") {
		t.Fatal("Add(a,5) 后应存活")
	}
	if err := s.Remove("a", 5); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	logStep(t, s, "Remove(a,5)", "删除时间5等于添加时间5，并列偏删除")
	if s.Contains("a") {
		t.Fatal("添加与删除时间戳相等时应视为删除")
	}

	// 反序到达：先删后加，时间戳相等仍偏删除。
	s2, _ := New("r2", 16)
	if err := s2.Remove("b", 7); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s2.Add("b", 7); err != nil {
		t.Fatalf("Add: %v", err)
	}
	logStep(t, s2, "Remove(b,7);Add(b,7)", "相等偏删除与到达顺序无关")
	if s2.Contains("b") {
		t.Fatal("先删后加且时间戳相等时应视为删除")
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestOutOfOrderTimestamps(t *testing.T) {
	s, _ := New("r1", 16)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("op: %v", err)
		}
	}
	must(s.Add("x", 10))
	must(s.Add("x", 3)) // 旧时间戳到达，不得把记录改小
	logStep(t, s, "Add(x,10);Add(x,3)", "添加记录取较大者 max(10,3)=10")
	rec, _ := s.Lookup("x")
	if rec.Add != 10 {
		t.Fatalf("旧添加时间戳把记录改小: got %d want 10", rec.Add)
	}
	must(s.Remove("x", 12))
	must(s.Remove("x", 4))
	logStep(t, s, "Remove(x,12);Remove(x,4)", "删除记录取较大者 max(12,4)=12")
	rec, _ = s.Lookup("x")
	if rec.Remove != 12 {
		t.Fatalf("旧删除时间戳把记录改小: got %d want 12", rec.Remove)
	}
	if s.Contains("x") {
		t.Fatal("删除时间12大于添加时间10，应不在集合中")
	}
	must(s.Add("x", 12)) // 与删除时间相等，并列偏删除
	logStep(t, s, "Add(x,12)", "添加12等于删除12，偏删除")
	if s.Contains("x") {
		t.Fatal("添加时间等于删除时间应视为删除")
	}
	must(s.Add("x", 13))
	logStep(t, s, "Add(x,13)", "添加13严格大于删除12，重新存活")
	if !s.Contains("x") {
		t.Fatal("添加时间严格大于删除时间应重新存活")
	}
}

func TestInvalidInputsRejected(t *testing.T) {
	if _, err := New("", 8); !errors.Is(err, ErrInvalidReplicaID) {
		t.Fatalf("空副本编号: got %v want %v", err, ErrInvalidReplicaID)
	}
	if _, err := New("r1", 0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("非正上限: got %v want %v", err, ErrInvalidLimit)
	}

	s, _ := New("r1", 2)
	if err := s.Add("a", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	other, _ := New("r2", 8)
	if err := other.Add("z", 9); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Merge(other); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	before := s.Snapshot()
	beforeSeq := s.Seq()
	beforePos := s.MergePosition("r2")

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"空串元素添加", func() error { return s.Add("", 1) }, ErrEmptyElement},
		{"空串元素删除", func() error { return s.Remove("", 1) }, ErrEmptyElement},
		{"零时间戳添加", func() error { return s.Add("b", 0) }, ErrNonPositiveTimestamp},
		{"负时间戳删除", func() error { return s.Remove("b", -3) }, ErrNonPositiveTimestamp},
		{"nil 源副本合并", func() error { return s.Merge(nil) }, ErrNilReplica},
		{"nil 源副本增量合并", func() error { return s.MergeIncremental(nil) }, ErrNilReplica},
		{"自身合并", func() error { return s.Merge(s) }, ErrSelfMerge},
		{"自身增量合并", func() error { return s.MergeIncremental(s) }, ErrSelfMerge},
		{"元素数超限添加", func() error { return s.Add("c", 1) }, ErrTooManyElements},
	}
	for _, tc := range cases {
		err := tc.op()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		t.Logf("拒绝场景=%s 原因=%v", tc.name, err)
	}

	// 超限合并：源含 3 个新元素，目标上限 2，必须整体拒绝。
	big, _ := New("r3", 8)
	for _, e := range []string{"p", "q", "w"} {
		if err := big.Add(e, 2); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := s.Merge(big); !errors.Is(err, ErrTooManyElements) {
		t.Fatalf("超限合并: got %v want %v", err, ErrTooManyElements)
	}
	if err := s.MergeIncremental(big); !errors.Is(err, ErrTooManyElements) {
		t.Fatalf("超限增量合并: got %v want %v", err, ErrTooManyElements)
	}

	// 所有被拒操作不得改变记录、变更序号或合并位置。
	if got := s.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("拒绝后记录被改变: got %v want %v", got, before)
	}
	if got := s.Seq(); got != beforeSeq {
		t.Fatalf("拒绝后变更序号被改变: got %d want %d", got, beforeSeq)
	}
	if got := s.MergePosition("r2"); got != beforePos {
		t.Fatalf("拒绝后合并位置被改变: got %d want %d", got, beforePos)
	}
	if got := s.MergePosition("r3"); got != 0 {
		t.Fatalf("被拒合并不得推进合并位置: got %d want 0", got)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("拒绝后状态不变 记录=%v 序号=%d 合并位置r2=%d", s.Snapshot(), s.Seq(), s.MergePosition("r2"))
}

func TestErrorsAreDistinct(t *testing.T) {
	all := []error{
		ErrNilReplica, ErrSelfMerge, ErrInvalidReplicaID, ErrEmptyElement,
		ErrNonPositiveTimestamp, ErrTooManyElements, ErrInvalidLimit,
	}
	for i := range all {
		for j := range all {
			if i != j && errors.Is(all[i], all[j]) {
				t.Fatalf("错误不可区分: %v 与 %v", all[i], all[j])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	s, _ := New("r1", 4)
	for i, e := range []string{"a", "b", "c"} {
		if err := s.Add(e, int64(i+1)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := s.Remove("a", 10); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := s.Elements(), []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements: got %v want %v", got, want)
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("Len: got %d want 2", got)
	}
}
