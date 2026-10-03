package org_test

import (
	"errors"
	"testing"

	"ontology/org"
)

func TestChainAndDefaults(t *testing.T) {
	o := org.New()
	if err := o.SetManager("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetManager("b", "c"); err != nil {
		t.Fatal(err)
	}
	got := o.Chain("a")
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("chain=%v want [b c]", got)
	}
	if o.Manager("ghost") != "" || o.Limit("ghost") != 0 {
		t.Fatal("unknown employee should have no manager and zero limit")
	}
}

func TestCycleRejected(t *testing.T) {
	o := org.New()
	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}} {
		if err := o.SetManager(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.SetManager("d", "a"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("long cycle: %v", err)
	}
	// 拒绝后图不变：d 仍无上级。
	if o.Manager("d") != "" {
		t.Fatalf("d manager=%q want empty after rejected cycle", o.Manager("d"))
	}
	// 间接成环：d->b 也会回到 a 链。
	if err := o.SetManager("d", "b"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("indirect cycle: %v", err)
	}
	if err := o.SetManager("a", "a"); !errors.Is(err, org.ErrCycle) {
		t.Fatalf("self loop: %v", err)
	}
}

func TestEmployeeCap(t *testing.T) {
	o := org.New()
	// 填入 MaxEmployees 个员工（用 SetLimit 登记）。
	for i := 0; i < org.MaxEmployees; i++ {
		if err := o.SetLimit(name(i), 1); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if err := o.SetLimit("onemore", 1); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("over cap limit: %v", err)
	}
	// 新上级会引入两个新员工，也必须拒绝。
	if err := o.SetManager("newA", "newB"); !errors.Is(err, org.ErrInvalid) {
		t.Fatalf("over cap manager: %v", err)
	}
	if o.Count() != org.MaxEmployees {
		t.Fatalf("count=%d", o.Count())
	}
	// 已存在员工改额度仍允许。
	if err := o.SetLimit(name(0), 2); err != nil {
		t.Fatalf("update existing: %v", err)
	}
}

func TestManagerAddsTwo(t *testing.T) {
	o := org.New()
	if err := o.SetManager("x", "y"); err != nil {
		t.Fatal(err)
	}
	if o.Count() != 2 {
		t.Fatalf("count=%d want 2", o.Count())
	}
	if o.Manager("y") != "" || o.Limit("y") != 0 {
		t.Fatal("new manager leaf should default to no manager / zero limit")
	}
}

func name(i int) string {
	if i == 0 {
		return "e0"
	}
	b := []byte{'e'}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(append(b, digits...))
}
