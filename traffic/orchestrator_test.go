package traffic

import (
	"errors"
	"fmt"
	"testing"
)

func mustNew(t *testing.T, b, h int, cd, p int64) *Orchestrator {
	t.Helper()
	o, err := New(b, h, cd, p)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", b, h, cd, p, err)
	}
	return o
}

func snap(o *Orchestrator) []Bucket {
	s := make([]Bucket, o.bucketsN)
	for i := range o.buckets {
		s[i] = Bucket{
			State:    o.buckets[i].state,
			Owner:    o.buckets[i].owner,
			Released: o.buckets[i].rel,
			K:        o.buckets[i].k,
		}
	}
	return s
}

func errIs(got, want error) bool { return errors.Is(got, want) }

// TestSpecExample 复刻题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	o := mustNew(t, 10, 2, 5, 1)

	if s, err := o.Claim("a", 3, 0); err != nil || s != 2 {
		t.Fatalf("Claim a = %d,%v want 2,nil", s, err)
	}
	if s, err := o.Claim("b", 2, 0); err != nil || s != 5 {
		t.Fatalf("Claim b = %d,%v want 5,nil", s, err)
	}
	if s, err := o.Resize("a", 1, 10); err != nil || s != 2 {
		t.Fatalf("Resize a 1 = %d,%v", s, err)
	}
	for _, i := range []int{3, 4} {
		b := o.buckets[i]
		if b.state != StateCooldown || b.owner != "a" || b.rel != 10 || b.k != 1 {
			t.Fatalf("bucket %d = %+v want cooldown(a,r=10,k=1)", i, b)
		}
	}
	if s, err := o.Claim("c", 2, 12); err != nil || s != 7 {
		t.Fatalf("Claim c = %d,%v want 7,nil", s, err)
	}
	if s, err := o.Resize("a", 3, 13); err != nil || s != 2 {
		t.Fatalf("Resize a 3 = %d,%v", s, err)
	}
	for _, i := range []int{2, 3, 4} {
		if o.buckets[i].state != StateHeld || o.buckets[i].owner != "a" {
			t.Fatalf("bucket %d not held by a", i)
		}
	}
	if err := o.Release("b", 14); err != nil {
		t.Fatalf("Release b: %v", err)
	}
	if _, err := o.Claim("d", 2, 18); !errIs(err, ErrCapacity) {
		t.Fatalf("Claim d@18 err = %v want ErrCapacity", err)
	}
	if s, err := o.Claim("d", 2, 19); err != nil || s != 5 {
		t.Fatalf("Claim d@19 = %d,%v want 5", s, err)
	}
}

// TestCooldownMultipliers 覆盖同一桶第 1..4 次冷却的等待倍数 1,2,3,3；cd=0 释放即可用。
func TestCooldownMultipliers(t *testing.T) {
	for _, cd := range []int64{0, 5} {
		t.Run(fmt.Sprintf("cd=%d", cd), func(t *testing.T) {
			o := mustNew(t, 4, 0, cd, 1)
			clock := int64(0)
			for cycle := 1; cycle <= 4; cycle++ {
				clock += 100
				// bucket0 每轮都由本人 x 立即收回（对 o 始终可用），
				// 其 k 只随 x 的 Release 累加，用来精确验证等待倍数。
				s, err := o.Claim("x", 1, clock)
				if err != nil {
					t.Fatalf("cycle %d claim: %v", cycle, err)
				}
				if s != 0 {
					t.Fatalf("cycle %d start = %d want 0", cycle, s)
				}
				if got := o.buckets[0].k; got != cycle-1 {
					t.Fatalf("bucket0 k after reclaim = %d want %d", got, cycle-1)
				}
				clock += 100
				if err := o.Release("x", clock); err != nil {
					t.Fatalf("release: %v", err)
				}
				mult := int64(cycle)
				if mult > 3 {
					mult = 3
				}
				avail := clock + cd*mult
				if b := o.buckets[0]; b.k != cycle {
					t.Fatalf("k = %d want %d", b.k, cycle)
				}
				if !o.availableTo(&o.buckets[0], "x", clock) {
					t.Fatalf("cycle %d: owner should always use own cooldown bucket", cycle)
				}
				if cd > 0 {
					if o.availableTo(&o.buckets[0], "other", avail-1) {
						t.Fatalf("cycle %d: available at %d, want blocked until %d", cycle, avail-1, avail)
					}
					if !o.availableTo(&o.buckets[0], "other", avail) {
						t.Fatalf("cycle %d: not available at exact %d", cycle, avail)
					}
					if o.availableTo(&o.buckets[0], "other", clock) {
						t.Fatalf("cycle %d: other should be blocked at release time", cycle)
					}
				} else {
					if !o.availableTo(&o.buckets[0], "other", clock) {
						t.Fatalf("cd=0: bucket available to others immediately at release time")
					}
				}
			}
		})
	}
}

// TestExactEqualityClaim 用真实 Claim 验证“差 1 截断、恰等取得”与 k 随重新分配保留。
func TestExactEqualityClaim(t *testing.T) {
	o := mustNew(t, 2, 0, 5, 1)
	if _, err := o.Claim("x", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := o.Release("x", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Claim("big", 2, 14); !errIs(err, ErrCapacity) {
		t.Fatalf("contiguous 2 blocked by cooldown, err=%v", err)
	}
	if s, err := o.Claim("y", 1, 14); err != nil || s != 1 {
		t.Fatalf("y@14 = %d,%v want start 1", s, err)
	}
	if s, err := o.Claim("z", 1, 15); err != nil || s != 0 {
		t.Fatalf("z@15 = %d,%v want exact-available bucket 0", s, err)
	}
	if o.buckets[0].k != 1 {
		t.Fatalf("k should survive reassignment, got %d", o.buckets[0].k)
	}
}
