package quota

import (
	"errors"
	"testing"
)

func TestSetQuotaTable(t *testing.T) {
	cases := []struct {
		name   string
		tenant string
		q      int64
		want   error
	}{
		{"空租户", "", 10, ErrInvalidQuota},
		{"负数额度", "t", -1, ErrInvalidQuota},
		{"超额上限", "t", 1e15 + 1, ErrInvalidQuota},
		{"正常额度", "t", 100, nil},
		{"零额度", "t", 0, nil},
		{"边界最大值", "t", 1e15, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := New()
			if got := l.SetQuota(c.tenant, c.q); !errors.Is(got, c.want) {
				t.Fatalf("SetQuota=%v want %v", got, c.want)
			}
		})
	}
}

func TestRejectLowerQuota(t *testing.T) {
	l := New()
	if err := l.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	if err := l.Reserve("A", 60); err != nil {
		t.Fatal(err)
	}
	if err := l.SetQuota("A", 59); !errors.Is(err, ErrBelowOccupied) {
		t.Fatalf("降额应拒绝低于占用, got %v", err)
	}
	// 恰等通过：U+R = 60。
	if err := l.SetQuota("A", 60); err != nil {
		t.Fatalf("恰等应通过, got %v", err)
	}
}

func TestReserveCommitRelease(t *testing.T) {
	l := New()
	if err := l.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	// 未设额度租户 q=0，预留必失败。
	if err := l.Reserve("B", 1); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("未设额度应不足, got %v", err)
	}
	if err := l.Reserve("A", 60); err != nil {
		t.Fatal(err)
	}
	if got := l.Info("A"); got != (Info{Quota: 100, Used: 0, Reserved: 60}) {
		t.Fatalf("Info=%+v", got)
	}
	if err := l.Reserve("A", 41); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("超预留应失败, got %v", err)
	}
	// 恰等通过。
	if err := l.Reserve("A", 40); err != nil {
		t.Fatal(err)
	}
	// 提交 60，覆盖旧对象 25：U=35, R=40。
	l.Commit("A", 60, 25)
	if got := l.Info("A"); got != (Info{Quota: 100, Used: 35, Reserved: 40}) {
		t.Fatalf("提交后 Info=%+v", got)
	}
	l.Release("A", 40)
	if got := l.Info("A"); got != (Info{Quota: 100, Used: 35, Reserved: 0}) {
		t.Fatalf("释放后 Info=%+v", got)
	}
}
