package resolve

import (
	"errors"
	"reflect"
	"testing"
)

type op struct {
	kind    byte // 'R' record, 'S' sync
	b, k, w int64
	payload string
	wantErr error
}

type wantEmit struct {
	payload string
	wall    int64
	source  Source
}

type step struct {
	op
	emits []wantEmit
}

func runSteps(t *testing.T, r *Resolver, dev int64, steps []step) {
	t.Helper()
	for i, st := range steps {
		var got []Emit
		var err error
		if st.kind == 'R' {
			got, err = r.Record(dev, st.b, st.k, st.payload)
		} else {
			got, err = r.Sync(dev, st.b, st.k, st.w)
		}
		if !errors.Is(err, st.wantErr) {
			t.Fatalf("step %d: err=%v want %v", i, err, st.wantErr)
		}
		if err != nil {
			continue
		}
		want := make([]Emit, len(st.emits))
		for j, e := range st.emits {
			want[j] = Emit{Payload: e.payload, Wall: e.wall, Source: e.source}
		}
		if len(want) == 0 {
			if len(got) != 0 {
				t.Fatalf("step %d: emits=%+v want none", i, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: emits=%+v want %+v", i, got, want)
		}
	}
}

func TestWorkedExample(t *testing.T) {
	r := New()
	if err := r.Register(1, 100); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r, 1, []step{
		{op: op{kind: 'R', b: 1, k: 100, payload: "r1"}},
		{op: op{kind: 'S', b: 1, k: 200, w: 5000}, emits: []wantEmit{{"r1", 4900, Back}}},
		{op: op{kind: 'R', b: 1, k: 300, payload: "r2"}},
		{op: op{kind: 'S', b: 1, k: 600, w: 5410}, emits: []wantEmit{{"r2", 5102, Interp}}},
		{op: op{kind: 'R', b: 1, k: 700, payload: "r3"}},
		{op: op{kind: 'R', b: 2, k: 50, payload: "q1"}, emits: []wantEmit{{"r3", 5510, Fwd}}},
		{op: op{kind: 'R', b: 2, k: 80, payload: "q2"}},
		{op: op{kind: 'R', b: 3, k: 10, payload: "z1"}},
		{op: op{kind: 'S', b: 3, k: 40, w: 9000}, emits: []wantEmit{
			{"z1", 8970, Back},
			{"q1", 8929, Est},
			{"q2", 8959, Est},
		}},
		{op: op{kind: 'S', b: 3, k: 20, w: 8990}},
		{op: op{kind: 'S', b: 3, k: 30, w: 8985, wantErr: ErrSkew}},
		{op: op{kind: 'R', b: 2, k: 90, payload: "q3", wantErr: ErrSealed}},
		{op: op{kind: 'S', b: 2, k: 10, w: 1, wantErr: ErrSealed}},
	})
}

func TestImmediateInterp(t *testing.T) {
	r := New()
	if err := r.Register(7, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(7, 1, 200, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(7, 1, 600, 5410); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r, 7, []step{
		{op: op{kind: 'R', b: 1, k: 250, payload: "x"}, emits: []wantEmit{{"x", 5051, Interp}}},
		{op: op{kind: 'R', b: 1, k: 600, payload: "y"}, emits: []wantEmit{{"y", 5410, Interp}}},
		{op: op{kind: 'R', b: 1, k: 100, payload: "z"}, emits: []wantEmit{{"z", 4900, Back}}},
		{op: op{kind: 'R', b: 1, k: 700, payload: "w"}},
	})
	if d := r.devs[7]; d.pending != 1 {
		t.Fatalf("pending=%d want 1 (no forward extrapolation)", d.pending)
	}
}

func TestDupKRecordNoDedup(t *testing.T) {
	r := New()
	if err := r.Register(1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sync(1, 1, 50, 1000); err != nil {
		t.Fatal(err)
	}
	out, err := r.Record(1, 1, 50, "a")
	if err != nil || len(out) != 1 {
		t.Fatalf("a: %v %+v", err, out)
	}
	out, err = r.Record(1, 1, 50, "b")
	if err != nil || len(out) != 1 || out[0].Payload != "b" {
		t.Fatalf("b: %v %+v", err, out)
	}
}

func TestGappedAndConsecutiveEstimBoots(t *testing.T) {
	// 出现 1、2、5（跳号）：5 首次同步时 2 视为紧邻前一个待估启动。
	r := New()
	if err := r.Register(1, 100); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r, 1, []step{
		{op: op{kind: 'R', b: 1, k: 5, payload: "b1"}},
		{op: op{kind: 'R', b: 2, k: 10, payload: "b2"}},
		{op: op{kind: 'R', b: 5, k: 20, payload: "b5"}},
		{op: op{kind: 'S', b: 5, k: 30, w: 1000}, emits: []wantEmit{
			{"b5", 990, Back},
			{"b2", 969, Est},
			{"b1", 958, Est},
		}},
	})
	// 连续两个待估 3、4，由 6 首次同步点反向依次估计 4、3。
	r2 := New()
	if err := r2.Register(1, 100); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r2, 1, []step{
		{op: op{kind: 'R', b: 3, k: 100, payload: "c3"}},
		{op: op{kind: 'R', b: 4, k: 80, payload: "c4"}},
		{op: op{kind: 'R', b: 6, k: 5, payload: "c6"}},
		{op: op{kind: 'S', b: 6, k: 10, w: 6000}, emits: []wantEmit{
			{"c6", 5995, Back},
			{"c4", 5989, Est},
			{"c3", 5908, Est},
		}},
	})
}

func TestSealedWithSyncsFwd(t *testing.T) {
	r := New()
	if err := r.Register(1, 100); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r, 1, []step{
		{op: op{kind: 'S', b: 1, k: 10, w: 1000}},
		{op: op{kind: 'R', b: 1, k: 30, payload: "f1"}},
		{op: op{kind: 'R', b: 1, k: 40, payload: "f2"}},
		{op: op{kind: 'R', b: 2, k: 0, payload: "n"}, emits: []wantEmit{
			{"f1", 1020, Fwd},
			{"f2", 1030, Fwd},
		}},
	})
}

func TestPmaxBoundaryAndFullRollback(t *testing.T) {
	r := New()
	if err := r.Register(1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(1, 1, 10, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(1, 1, 20, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(1, 1, 30, "c"); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v", err)
	}
	// 可直接输出的记录不受 Pmax 限制（只有后继 -> Back）。
	out, err := r.Sync(1, 1, 40, 5000)
	if err != nil || len(out) != 2 {
		t.Fatalf("sync releases pending: %v %+v", err, out)
	}
	if d := r.devs[1]; d.pending != 0 {
		t.Fatalf("pending=%d want 0", d.pending)
	}
	out, err = r.Record(1, 1, 5, "d")
	if err != nil || len(out) != 1 || out[0].Source != Back || out[0].Wall != 4965 {
		t.Fatalf("direct emit despite full: %v %+v", err, out)
	}
}

func TestFullRollbackIncludesSeal(t *testing.T) {
	// Pmax=1：boot1 无同步点、1 条待定；Record 到 boot2 需先封口（待估分支，
	// 不释放名额），随后新记录需待定但已满 -> ErrFull，封口整体回滚。
	r := New()
	if err := r.Register(1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(1, 1, 10, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(1, 2, 20, "b"); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v", err)
	}
	out, err := r.Sync(1, 1, 50, 5000)
	if err != nil {
		t.Fatalf("sync after rollback: %v", err)
	}
	if len(out) != 1 || out[0].Payload != "a" || out[0].Source != Back {
		t.Fatalf("emit=%+v", out)
	}
}

func TestErrorPriority(t *testing.T) {
	r := New()
	if err := r.Register(1, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Record(99, 1, 0, nil); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("unregistered: %v", err)
	}
	if _, err := r.Record(99, 0, -1, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid beats nodevice: %v", err)
	}
	if err := r.Register(1, 9); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dup register: %v", err)
	}
	if err := r.Register(2, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad pmax: %v", err)
	}
	if _, err := r.Record(1, 2, 0, "x"); err != nil { // 封口 boot1（待估）
		t.Fatal(err)
	}
	if _, err := r.Sync(1, 1, 10, 100); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed beats other errors: %v", err)
	}
}

func TestEstimationCanBeNegative(t *testing.T) {
	r := New()
	if err := r.Register(1, 10); err != nil {
		t.Fatal(err)
	}
	runSteps(t, r, 1, []step{
		{op: op{kind: 'R', b: 1, k: 1000, payload: "a"}},
		{op: op{kind: 'R', b: 2, k: 0, payload: "b"}},
		{op: op{kind: 'S', b: 2, k: 0, w: 100}, emits: []wantEmit{
			{"b", 100, Interp},
			{"a", 99, Est}, // S_2=100; S_1=100-1-1000=-901; -901+1000=99
		}},
	})
}

func TestExaminedCount(t *testing.T) {
	for _, total := range []int{100, 10000} {
		r := New()
		if err := r.Register(1, total+10); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < total; i++ {
			if _, err := r.Record(1, 1, int64(100+i), i); err != nil {
				t.Fatal(err)
			}
		}
		// 同步点 k=102 释放恰好 3 条（k=100,101,102），其余全部待定。
		if _, err := r.Sync(1, 1, 102, 1_000_000); err != nil {
			t.Fatal(err)
		}
		d := r.devs[1]
		if d.released != 3 {
			t.Fatalf("total=%d: released=%d", total, d.released)
		}
		if d.examined > d.released+1 {
			t.Fatalf("total=%d: examined=%d released=%d", total, d.examined, d.released)
		}
		if d.examined != 4 {
			t.Fatalf("total=%d: examined=%d want 4", total, d.examined)
		}
		if d.pending != total-3 {
			t.Fatalf("total=%d: pending=%d", total, d.pending)
		}
	}
}
