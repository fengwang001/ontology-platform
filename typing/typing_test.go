package typing

import (
	"reflect"
	"testing"

	"ontology/bloodstock"
)

func slot(a bloodstock.ABO, r bloodstock.Rh) int { return bloodstock.Slot(a, r) }

func TestStateTransitions(t *testing.T) {
	r := NewRegistry()

	if k, _ := r.State("P"); k != Unknown {
		t.Fatal("零次为未知")
	}
	k, _ := r.Type(1, "P", "s1", bloodstock.A, bloodstock.Positive)
	if k != Single {
		t.Fatal("一次为单次")
	}
	k, _ = r.Type(2, "P", "s2", bloodstock.A, bloodstock.Positive)
	if k != Confirmed {
		t.Fatal("两次一致为已确认")
	}
	// 第三次仍一致
	k, _ = r.Type(3, "P", "s3", bloodstock.A, bloodstock.Positive)
	if k != Confirmed {
		t.Fatal("多次一致应保持已确认")
	}
	// 出现不一致 -> 存疑（粘滞）
	k, contrad := r.Type(4, "P", "s4", bloodstock.B, bloodstock.Positive)
	if k != Disputed || !contrad {
		t.Fatal("不一致应转存疑并返回 contrad")
	}
	// 此后再给回 A+ 也不恢复
	k, _ = r.Type(5, "P", "s5", bloodstock.A, bloodstock.Positive)
	if k != Disputed {
		t.Fatal("存疑应粘滞")
	}
	// 主管裁定恢复
	if r.Resolve("P", bloodstock.A, bloodstock.Positive) != Confirmed {
		t.Fatal("Resolve 应转已确认")
	}
	if k, _ := r.State("P"); k != Confirmed {
		t.Fatal("裁定后应已确认")
	}
}

func TestOrderTable(t *testing.T) {
	cases := []struct {
		name string
		run  func(r *Registry)
		want []int
	}{
		{"未知", func(r *Registry) {}, []int{slot(bloodstock.O, bloodstock.Negative)}},
		{"单次A+只用O_先阳后阴", func(r *Registry) {
			r.Type(1, "Q", "s", bloodstock.A, bloodstock.Positive)
		}, []int{slot(bloodstock.O, bloodstock.Positive), slot(bloodstock.O, bloodstock.Negative)}},
		{"单次A-只用O阴", func(r *Registry) {
			r.Type(1, "Q", "s", bloodstock.A, bloodstock.Negative)
		}, []int{slot(bloodstock.O, bloodstock.Negative)}},
		{"存疑只用O阴", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.A, bloodstock.Positive)
			r.Type(2, "Q", "s2", bloodstock.B, bloodstock.Positive)
		}, []int{slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认A+", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.A, bloodstock.Positive)
			r.Type(2, "Q", "s2", bloodstock.A, bloodstock.Positive)
		}, []int{slot(bloodstock.A, bloodstock.Positive), slot(bloodstock.A, bloodstock.Negative),
			slot(bloodstock.O, bloodstock.Positive), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认A-", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.A, bloodstock.Negative)
			r.Type(2, "Q", "s2", bloodstock.A, bloodstock.Negative)
		}, []int{slot(bloodstock.A, bloodstock.Negative), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认B+", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.B, bloodstock.Positive)
			r.Type(2, "Q", "s2", bloodstock.B, bloodstock.Positive)
		}, []int{slot(bloodstock.B, bloodstock.Positive), slot(bloodstock.B, bloodstock.Negative),
			slot(bloodstock.O, bloodstock.Positive), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认AB+_ABO先于Rh", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.AB, bloodstock.Positive)
			r.Type(2, "Q", "s2", bloodstock.AB, bloodstock.Positive)
		}, []int{slot(bloodstock.AB, bloodstock.Positive), slot(bloodstock.AB, bloodstock.Negative),
			slot(bloodstock.A, bloodstock.Positive), slot(bloodstock.A, bloodstock.Negative),
			slot(bloodstock.B, bloodstock.Positive), slot(bloodstock.B, bloodstock.Negative),
			slot(bloodstock.O, bloodstock.Positive), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认AB-", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.AB, bloodstock.Negative)
			r.Type(2, "Q", "s2", bloodstock.AB, bloodstock.Negative)
		}, []int{slot(bloodstock.AB, bloodstock.Negative), slot(bloodstock.A, bloodstock.Negative),
			slot(bloodstock.B, bloodstock.Negative), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认O+", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.O, bloodstock.Positive)
			r.Type(2, "Q", "s2", bloodstock.O, bloodstock.Positive)
		}, []int{slot(bloodstock.O, bloodstock.Positive), slot(bloodstock.O, bloodstock.Negative)}},
		{"已确认O-", func(r *Registry) {
			r.Type(1, "Q", "s1", bloodstock.O, bloodstock.Negative)
			r.Type(2, "Q", "s2", bloodstock.O, bloodstock.Negative)
		}, []int{slot(bloodstock.O, bloodstock.Negative)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			c.run(r)
			got := r.Order("Q")
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got=%v want=%v", got, c.want)
			}
		})
	}
}
