package api_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/api"
)

func ids(n int) []string {
	s := make([]string, n)
	for i := range s {
		s[i] = fmt.Sprintf("p%d", i)
	}
	return s
}

func arriveAll(t *testing.T, b *api.Barrier, n int) {
	t.Helper()
	for _, id := range ids(n) {
		if err := b.Arrive(id); err != nil {
			t.Fatalf("arrive %s: %v", id, err)
		}
	}
}

// 不变量 1：到齐才释放。
func TestReleaseOnlyWhenFull(t *testing.T) {
	for _, n := range []int{1, 2, 3, 17, 100} {
		b := api.New(n)
		for i, id := range ids(n) {
			if err := b.Arrive(id); err != nil {
				t.Fatalf("n=%d arrive %s: %v", n, id, err)
			}
			if want := i == n-1; b.Released() != want {
				t.Fatalf("n=%d: released=%v after %d arrivals, want %v", n, b.Released(), i+1, want)
			}
		}
	}
}

// 不变量 2：离开才推进。
func TestAdvanceOnlyWhenAllDepart(t *testing.T) {
	for _, n := range []int{1, 2, 3, 17, 100} {
		b := api.New(n)
		arriveAll(t, b, n)
		for i, id := range ids(n) {
			if err := b.Depart(id); err != nil {
				t.Fatalf("n=%d depart: %v", n, err)
			}
			if i < n-1 && b.Round() != 0 {
				t.Fatalf("n=%d: round advanced after %d departs", n, i+1)
			}
		}
		if b.Round() != 1 || b.Released() {
			t.Fatalf("n=%d: round=%d released=%v, want 1/false", n, b.Round(), b.Released())
		}
	}
}

// 不变量 3：轮次隔离——离开后本轮未结束不得再次 Arrive。
func TestRoundIsolation(t *testing.T) {
	b := api.New(3)
	arriveAll(t, b, 3)
	if err := b.Depart("p0"); err != nil {
		t.Fatalf("depart: %v", err)
	}
	if err := b.Arrive("p0"); !errors.Is(err, api.ErrArriveAfterDepart) {
		t.Fatalf("got %v, want ErrArriveAfterDepart", err)
	}
	if b.Round() != 0 {
		t.Fatalf("round advanced to %d despite rejection", b.Round())
	}
}

// 不变量 4：失败不留痕——四类故障注入均被拒、互不相同、状态不变。
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		seq  func(b *api.Barrier) error
		want error
	}{
		{"空ID", func(b *api.Barrier) error { return b.Arrive("") }, api.ErrEmptyID},
		{"重复Arrive", func(b *api.Barrier) error {
			_ = b.Arrive("x")
			return b.Arrive("x")
		}, api.ErrDuplicateArrive},
		{"未到齐Depart", func(b *api.Barrier) error { return b.Depart("x") }, api.ErrEarlyDepart},
		{"离开后Arrive", func(b *api.Barrier) error {
			_ = b.Arrive("x")
			_ = b.Arrive("y")
			_ = b.Depart("x")
			return b.Arrive("x")
		}, api.ErrArriveAfterDepart},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := api.New(2)
			if err := c.seq(b); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if seen[c.want] {
				t.Fatalf("error %v reused across fault kinds", c.want)
			}
			seen[c.want] = true
			round, released := b.Round(), b.Released()
			if err := c.seq(b); !errors.Is(err, c.want) {
				t.Fatalf("replay: got %v, want %v", err, c.want)
			}
			if b.Round() != round || b.Released() != released {
				t.Fatal("rejected op changed state")
			}
		})
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New(3).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
