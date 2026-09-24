package api_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/api"
)

// 十三步序列（对应 NOTES.md 分步表）：逐步核验 C、Pending 与逐步结果。
func TestThirteenSteps(t *testing.T) {
	c := api.New()
	steps := []struct {
		op       func() error
		wantC    int64
		wantPend []int64
		wantErr  error
	}{
		{func() error { return c.Apply(1, 10) }, 0, []int64{1}, nil},
		{func() error { return c.Commit(1) }, 1, []int64{}, nil},
		{func() error { return c.Apply(2, 20) }, 1, []int64{2}, nil},
		{func() error { return c.Apply(2, 20) }, 1, []int64{2}, nil},
		{func() error { return c.Restart() }, 1, []int64{2}, nil},
		{func() error { return c.Apply(2, 20) }, 1, []int64{2}, nil},
		{func() error { return c.Commit(2) }, 2, []int64{}, nil},
		{func() error { return c.Commit(4) }, 2, []int64{}, api.ErrOffsetJump},
		{func() error { return c.Apply(3, 30) }, 2, []int64{3}, nil},
		{func() error { return c.Commit(3) }, 3, []int64{}, nil},
		{func() error { return c.Commit(4) }, 3, []int64{}, api.ErrEffectMissing},
		{func() error { return c.Apply(4, 40) }, 3, []int64{4}, nil},
		{func() error { return c.Commit(4) }, 4, []int64{}, nil},
	}
	for i, s := range steps {
		if err := s.op(); !errors.Is(err, s.wantErr) {
			t.Fatalf("step %d: got err %v want %v", i+1, err, s.wantErr)
		}
		if c.Committed() != s.wantC {
			t.Fatalf("step %d: C=%d want %d", i+1, c.Committed(), s.wantC)
		}
		if pend := c.Pending(); !reflect.DeepEqual(pend, s.wantPend) {
			t.Fatalf("step %d: Pending=%v want %v", i+1, pend, s.wantPend)
		}
	}
}

// 四类故障注入：错误可判定、互不相同，被拒后状态不变且可继续使用。
func TestFaultInjection(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  int64
		op   func(c *api.Committer, seq int64) error
		want error
	}{
		{"out-of-order apply", 5, func(c *api.Committer, s int64) error { return c.Apply(s, 1) }, api.ErrOutOfOrder},
		{"missing-effect commit", 1, func(c *api.Committer, s int64) error { return c.Commit(s) }, api.ErrEffectMissing},
		{"offset-jump commit", 9, func(c *api.Committer, s int64) error { return c.Commit(s) }, api.ErrOffsetJump},
		{"invalid seq apply", 0, func(c *api.Committer, s int64) error { return c.Apply(s, 1) }, api.ErrInvalidSeq},
		{"invalid seq commit", -1, func(c *api.Committer, s int64) error { return c.Commit(s) }, api.ErrInvalidSeq},
	} {
		c := api.New()
		if err := tc.op(c, tc.seq); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		if c.Committed() != 0 || len(c.Pending()) != 0 {
			t.Fatalf("%s: rejected op left trace", tc.name)
		}
		if err := c.Apply(1, 1); err != nil { // 被拒后仍可正常使用
			t.Fatalf("%s: unusable after rejection: %v", tc.name, err)
		}
	}
	// 互不相同
	set := map[error]bool{api.ErrInvalidSeq: true, api.ErrOutOfOrder: true,
		api.ErrEffectMissing: true, api.ErrOffsetJump: true}
	if len(set) != 4 {
		t.Fatal("sentinel errors not distinct")
	}
}

// Restart 返回的 Pending 驱动至少一次重放：重启后重放 pending 再提交。
func TestRestartAtLeastOnce(t *testing.T) {
	c := api.New()
	c.Apply(1, 10)
	c.Commit(1)
	c.Apply(2, 20) // 崩溃：效果已写、位点未提交
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	if pend := c.Pending(); !reflect.DeepEqual(pend, []int64{2}) {
		t.Fatalf("Pending=%v want [2]", pend)
	}
	for _, seq := range c.Pending() { // 消费端重复处理
		c.Apply(seq, 20)
		c.Commit(seq)
	}
	if c.Committed() != 2 || len(c.Pending()) != 0 {
		t.Fatalf("C=%d Pending=%v", c.Committed(), c.Pending())
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
