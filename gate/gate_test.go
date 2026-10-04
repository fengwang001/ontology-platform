package gate_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/compat"
	"ontology/gate"
	"ontology/schema"
)

func fld(name string, typ schema.Type, required, hasDefault bool) schema.Field {
	return schema.Field{Name: name, Type: typ, Required: required, HasDefault: hasDefault}
}

func mustCreate(t *testing.T, g *gate.Gate, subj string, mode schema.Mode, fields []schema.Field, now int64) {
	t.Helper()
	if v, err := g.CreateSubject(subj, mode, fields, now); err != nil || v != 1 {
		t.Fatalf("CreateSubject(%q) = %d, %v", subj, v, err)
	}
}

func mustSubscribe(t *testing.T, g *gate.Gate, subj, consumer string, pinned int, fields []string, now int64) {
	t.Helper()
	if err := g.Subscribe(subj, consumer, pinned, fields, now); err != nil {
		t.Fatalf("Subscribe(%q, %q) = %v", subj, consumer, err)
	}
}

func mustStatus(t *testing.T, g *gate.Gate, subj, consumer string, want gate.Status) {
	t.Helper()
	got, err := g.Status(subj, consumer)
	if err != nil {
		t.Fatalf("Status(%q, %q) = %v", subj, consumer, err)
	}
	if got != want {
		t.Fatalf("Status(%q, %q) = %+v, want %+v", subj, consumer, got, want)
	}
}

func mustLatest(t *testing.T, g *gate.Gate, subj string, want int) {
	t.Helper()
	got, err := g.Latest(subj)
	if err != nil {
		t.Fatalf("Latest(%q) = %v", subj, err)
	}
	if got != want {
		t.Fatalf("Latest(%q) = %d, want %d", subj, got, want)
	}
}

func assertBlocked(t *testing.T, err error, want []gate.Blocker) {
	t.Helper()
	var be *gate.BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want ErrBlocked %v", err, want)
	}
	if !reflect.DeepEqual(be.Blockers, want) {
		t.Fatalf("blockers = %v, want %v", be.Blockers, want)
	}
}

func assertIncompatible(t *testing.T, err error, dir schema.Mode, want compat.Violation) {
	t.Helper()
	var ie *gate.IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want ErrIncompatible %v %v", err, dir, want)
	}
	if ie.Direction != dir || ie.Violation != want {
		t.Fatalf("incompatible = %v %v, want %v %v", ie.Direction, ie.Violation, dir, want)
	}
}

// 规格样例：X 被 c1/c2 阻塞，Y 被 c2 阻塞，豁免后放行且 c2 变 Lagging，最后 Advance 恢复。
func TestSpecExample(t *testing.T) {
	g := gate.New()
	v1 := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("name", schema.String, true, false),
		fld("age", schema.Int32, false, false),
	}
	mustCreate(t, g, "s", schema.Backward, v1, 0)
	mustSubscribe(t, g, "s", "c1", 1, []string{"id", "name"}, 1)
	mustSubscribe(t, g, "s", "c2", 1, []string{"id", "age"}, 2)

	X := []schema.Field{
		fld("id", schema.Int64, true, false),
		fld("name", schema.String, true, false),
		fld("email", schema.String, true, true),
	}
	_, err := g.Publish("s", X, 3)
	assertBlocked(t, err, []gate.Blocker{
		{Consumer: "c1", Violation: compat.Violation{Field: "id", Reason: compat.TypeMismatch}},
		{Consumer: "c2", Violation: compat.Violation{Field: "id", Reason: compat.TypeMismatch}},
	})

	Y := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("name", schema.String, true, false),
		fld("email", schema.String, true, true),
	}
	_, err = g.Publish("s", Y, 4)
	assertBlocked(t, err, []gate.Blocker{
		{Consumer: "c2", Violation: compat.Violation{Field: "age", Reason: compat.MissingNoDefault}},
	})
	mustLatest(t, g, "s", 1) // 被拒不占号

	if err := g.Waive("s", "c2", 50, 45); err != nil {
		t.Fatalf("Waive = %v", err)
	}
	ver, err := g.Publish("s", Y, 49)
	if err != nil || ver != 2 {
		t.Fatalf("Publish(Y) at 49 = %d, %v, want v2", ver, err)
	}
	mustStatus(t, g, "s", "c2", gate.Status{Pinned: 1, Lagging: true, LaggingVersion: 2})
	mustStatus(t, g, "s", "c1", gate.Status{Pinned: 1})

	if err := g.Advance("s", "c2", 2, []string{"id", "email"}, 60); err != nil {
		t.Fatalf("Advance = %v", err)
	}
	mustStatus(t, g, "s", "c2", gate.Status{Pinned: 2})
}

// 豁免边界：until=50 时 now=49（小 1）放行、now=50（恰等）仍阻塞。
func TestWaiverBoundary(t *testing.T) {
	newGate := func(t *testing.T) *gate.Gate {
		g := gate.New()
		mustCreate(t, g, "s", schema.Backward, []schema.Field{
			fld("id", schema.Int32, true, false),
			fld("age", schema.Int32, false, false),
		}, 0)
		mustSubscribe(t, g, "s", "c2", 1, []string{"id", "age"}, 1)
		if err := g.Waive("s", "c2", 50, 10); err != nil {
			t.Fatalf("Waive = %v", err)
		}
		return g
	}
	Y := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("email", schema.String, true, true),
	}
	g := newGate(t)
	ver, err := g.Publish("s", Y, 49)
	if err != nil || ver != 2 {
		t.Fatalf("Publish at until-1 = %d, %v, want v2", ver, err)
	}
	mustStatus(t, g, "s", "c2", gate.Status{Pinned: 1, Lagging: true, LaggingVersion: 2})

	g = newGate(t)
	_, err = g.Publish("s", Y, 50)
	assertBlocked(t, err, []gate.Blocker{
		{Consumer: "c2", Violation: compat.Violation{Field: "age", Reason: compat.MissingNoDefault}},
	})
	mustStatus(t, g, "s", "c2", gate.Status{Pinned: 1})
	mustLatest(t, g, "s", 1)
}

// FORWARD 主题在第 (2) 步即报 ErrIncompatible，不进入订阅者检查。
func TestSpecExampleForward(t *testing.T) {
	g := gate.New()
	v1 := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("name", schema.String, true, false),
		fld("age", schema.Int32, false, false),
	}
	mustCreate(t, g, "s", schema.Forward, v1, 0)
	mustSubscribe(t, g, "s", "c1", 1, []string{"id", "name"}, 1)
	Y := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("name", schema.String, true, false),
		fld("email", schema.String, true, true),
	}
	_, err := g.Publish("s", Y, 2)
	assertIncompatible(t, err, schema.Forward, compat.Violation{Field: "age", Reason: compat.MissingNoDefault})
	mustLatest(t, g, "s", 1)
}

// 同一变更在 BACKWARD 与 FORWARD 下结论相反。
func TestModeDirectionOpposite(t *testing.T) {
	tests := []struct {
		name    string
		v1, N   []schema.Field
		mode    schema.Mode
		wantErr bool
		dir     schema.Mode
		vio     compat.Violation
	}{
		{
			name: "widen int32->int64 BACKWARD ok",
			v1:   []schema.Field{fld("id", schema.Int32, true, false)},
			N:    []schema.Field{fld("id", schema.Int64, true, false)},
			mode: schema.Backward,
		},
		{
			name:    "widen int32->int64 FORWARD rejected",
			v1:      []schema.Field{fld("id", schema.Int32, true, false)},
			N:       []schema.Field{fld("id", schema.Int64, true, false)},
			mode:    schema.Forward,
			wantErr: true,
			dir:     schema.Forward,
			vio:     compat.Violation{Field: "id", Reason: compat.TypeMismatch},
		},
		{
			name:    "narrow int64->int32 BACKWARD rejected",
			v1:      []schema.Field{fld("id", schema.Int64, true, false)},
			N:       []schema.Field{fld("id", schema.Int32, true, false)},
			mode:    schema.Backward,
			wantErr: true,
			dir:     schema.Backward,
			vio:     compat.Violation{Field: "id", Reason: compat.TypeMismatch},
		},
		{
			name: "narrow int64->int32 FORWARD ok",
			v1:   []schema.Field{fld("id", schema.Int64, true, false)},
			N:    []schema.Field{fld("id", schema.Int32, true, false)},
			mode: schema.Forward,
		},
		{
			name: "string->bytes BACKWARD ok",
			v1:   []schema.Field{fld("s", schema.String, true, false)},
			N:    []schema.Field{fld("s", schema.Bytes, true, false)},
			mode: schema.Backward,
		},
		{
			name:    "string->bytes FORWARD rejected",
			v1:      []schema.Field{fld("s", schema.String, true, false)},
			N:       []schema.Field{fld("s", schema.Bytes, true, false)},
			mode:    schema.Forward,
			wantErr: true,
			dir:     schema.Forward,
			vio:     compat.Violation{Field: "s", Reason: compat.TypeMismatch},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gate.New()
			mustCreate(t, g, "s", tt.mode, tt.v1, 0)
			ver, err := g.Publish("s", tt.N, 1)
			if !tt.wantErr {
				if err != nil || ver != 2 {
					t.Fatalf("Publish = %d, %v, want v2", ver, err)
				}
				return
			}
			assertIncompatible(t, err, tt.dir, tt.vio)
			mustLatest(t, g, "s", 1)
		})
	}
}

// 删除无人订阅的字段放行；有人订阅则阻塞。
func TestDeleteField(t *testing.T) {
	v1 := []schema.Field{
		fld("id", schema.Int32, true, false),
		fld("extra", schema.String, true, false),
	}
	N := []schema.Field{fld("id", schema.Int32, true, false)}

	g := gate.New()
	mustCreate(t, g, "s", schema.Backward, v1, 0)
	mustSubscribe(t, g, "s", "c1", 1, []string{"id"}, 1)
	if ver, err := g.Publish("s", N, 2); err != nil || ver != 2 {
		t.Fatalf("Publish dropping unsubscribed field = %d, %v, want v2", ver, err)
	}

	g = gate.New()
	mustCreate(t, g, "s", schema.Backward, v1, 0)
	mustSubscribe(t, g, "s", "c2", 1, []string{"extra"}, 1)
	_, err := g.Publish("s", N, 2)
	assertBlocked(t, err, []gate.Blocker{
		{Consumer: "c2", Violation: compat.Violation{Field: "extra", Reason: compat.MissingNoDefault}},
	})
}

// Lagging 的订阅者在后续 Publish 中既不检查也不需要豁免。
func TestLaggingNoLongerBlocks(t *testing.T) {
	g := gate.New()
	mustCreate(t, g, "s", schema.Backward, []schema.Field{
		fld("a", schema.Int32, true, false),
		fld("b", schema.Int32, true, false),
	}, 0)
	mustSubscribe(t, g, "s", "c1", 1, []string{"a", "b"}, 1)
	if err := g.Waive("s", "c1", 100, 2); err != nil {
		t.Fatalf("Waive = %v", err)
	}
	// v2：a 加宽，c1 违规但被豁免 → Lagging(2)。
	if ver, err := g.Publish("s", []schema.Field{
		fld("a", schema.Int64, true, false),
		fld("b", schema.Int32, true, false),
	}, 3); err != nil || ver != 2 {
		t.Fatalf("Publish v2 = %d, %v", ver, err)
	}
	mustStatus(t, g, "s", "c1", gate.Status{Pinned: 1, Lagging: true, LaggingVersion: 2})
	// v3：删除 b。c1 已 Lagging 不再检查，否则 b MissingNoDefault 必阻塞。
	if ver, err := g.Publish("s", []schema.Field{
		fld("a", schema.Int64, true, false),
	}, 4); err != nil || ver != 3 {
		t.Fatalf("Publish v3 = %d, %v", ver, err)
	}
	mustStatus(t, g, "s", "c1", gate.Status{Pinned: 1, Lagging: true, LaggingVersion: 2})
}

// Advance 失败不改状态；成功后清除 Lagging 与豁免。
func TestAdvance(t *testing.T) {
	g := gate.New()
	mustCreate(t, g, "s", schema.Backward, []schema.Field{
		fld("a", schema.Int32, true, false),
		fld("b", schema.Int32, true, false),
	}, 0)
	mustSubscribe(t, g, "s", "c1", 1, []string{"a"}, 1)
	if err := g.Waive("s", "c1", 100, 2); err != nil {
		t.Fatalf("Waive = %v", err)
	}
	// v2：a 加宽，c1 豁免放行 → Lagging(2)。
	if _, err := g.Publish("s", []schema.Field{
		fld("a", schema.Int64, true, false),
		fld("b", schema.Int32, true, false),
	}, 3); err != nil {
		t.Fatalf("Publish v2 = %v", err)
	}
	// v3：b 也加宽；c1 Lagging 不参与检查。
	if _, err := g.Publish("s", []schema.Field{
		fld("a", schema.Int64, true, false),
		fld("b", schema.Int64, true, false),
	}, 4); err != nil {
		t.Fatalf("Publish v3 = %v", err)
	}
	// 失败：to 不大于 pinned（归入版本不存在）。
	if err := g.Advance("s", "c1", 1, []string{"a"}, 5); !errors.Is(err, gate.ErrVersionNotFound) {
		t.Fatalf("Advance to=1 = %v, want ErrVersionNotFound", err)
	}
	// 失败：to 超过 latest。
	if err := g.Advance("s", "c1", 4, []string{"a"}, 5); !errors.Is(err, gate.ErrVersionNotFound) {
		t.Fatalf("Advance to=4 = %v, want ErrVersionNotFound", err)
	}
	// 失败：fields 指向版本 to 中不存在的字段。
	if err := g.Advance("s", "c1", 2, []string{"zz"}, 5); !errors.Is(err, gate.ErrFieldNotFound) {
		t.Fatalf("Advance fields=zz = %v, want ErrFieldNotFound", err)
	}
	// 失败：新视图（v2 的 b 是 int32）仍读不了 latest（b 是 int64）→ ErrStillBroken，状态不变。
	var sb *gate.StillBrokenError
	if err := g.Advance("s", "c1", 2, []string{"b"}, 5); !errors.As(err, &sb) {
		t.Fatalf("Advance to=2 fields=b = %v, want ErrStillBroken", err)
	}
	mustStatus(t, g, "s", "c1", gate.Status{Pinned: 1, Lagging: true, LaggingVersion: 2})
	// 成功：清除 Lagging 与豁免。
	if err := g.Advance("s", "c1", 3, []string{"a"}, 6); err != nil {
		t.Fatalf("Advance to=3 = %v", err)
	}
	mustStatus(t, g, "s", "c1", gate.Status{Pinned: 3})
	// 豁免已清除：发布删 a 的版本（BACKWARD 模式检查通过）会被 c1 阻塞。
	_, err := g.Publish("s", []schema.Field{fld("b", schema.Int64, true, false)}, 7)
	assertBlocked(t, err, []gate.Blocker{
		{Consumer: "c1", Violation: compat.Violation{Field: "a", Reason: compat.MissingNoDefault}},
	})
}

// ErrNoChange 先于兼容检查；被拒操作不推进时钟与版本号。
func TestNoChange(t *testing.T) {
	g := gate.New()
	v1 := []schema.Field{fld("a", schema.Int32, true, false), fld("b", schema.String, false, true)}
	mustCreate(t, g, "s", schema.Full, v1, 10)
	// 逐字段全同（另一个切片实例）→ ErrNoChange。
	dup := []schema.Field{fld("a", schema.Int32, true, false), fld("b", schema.String, false, true)}
	if _, err := g.Publish("s", dup, 20); !errors.Is(err, gate.ErrNoChange) {
		t.Fatalf("Publish identical = %v, want ErrNoChange", err)
	}
	// 时钟回退（层级更高）先于 ErrNoChange。
	if _, err := g.Publish("s", dup, 5); !errors.Is(err, gate.ErrClockRegression) {
		t.Fatalf("Publish identical regressive = %v, want ErrClockRegression", err)
	}
	// 被拒的 ErrNoChange（now=20）不推进时钟：now=15 仍可用；也不占版本号。
	if ver, err := g.Publish("s", []schema.Field{
		fld("a", schema.Int32, true, false),
		fld("b", schema.String, false, true),
		fld("c", schema.Int32, false, true),
	}, 15); err != nil || ver != 2 {
		t.Fatalf("Publish = %d, %v, want v2", ver, err)
	}
	// 字段顺序不同不算 NoChange（逐字段比较）。
	if _, err := g.Publish("s", []schema.Field{
		fld("b", schema.String, false, true),
		fld("a", schema.Int32, true, false),
	}, 16); err != nil {
		t.Fatalf("Publish reordered = %v, want nil", err)
	}
	mustLatest(t, g, "s", 3)
}

// 拒绝次序：参数非法 > 时钟回退 > 主题不存在 > 已存在/订阅者不存在 >
// 版本或字段不存在 > ErrNoChange > ErrIncompatible > ErrBlocked/ErrStillBroken。
func TestRejectionOrder(t *testing.T) {
	v1 := []schema.Field{fld("a", schema.Int32, true, false), fld("b", schema.Int32, true, false)}
	base := func(t *testing.T, g *gate.Gate) { t.Helper(); mustCreate(t, g, "s", schema.Backward, v1, 10) }
	tests := []struct {
		name  string
		setup func(t *testing.T, g *gate.Gate)
		op    func(g *gate.Gate) error
		want  error // nil 表示期望成功
	}{
		{
			name:  "invalid fields before clock regression",
			setup: base,
			op: func(g *gate.Gate) error {
				_, err := g.Publish("s", []schema.Field{
					fld("a", schema.Int32, true, false),
					fld("a", schema.Int64, false, false),
				}, 5)
				return err
			},
			want: gate.ErrInvalid,
		},
		{
			name: "unknown mode",
			op: func(g *gate.Gate) error {
				_, err := g.CreateSubject("s2", schema.Mode(9), v1, 0)
				return err
			},
			want: gate.ErrInvalid,
		},
		{
			name: "now out of range",
			op: func(g *gate.Gate) error {
				_, err := g.CreateSubject("s2", schema.Backward, v1, schema.MaxNow+1)
				return err
			},
			want: gate.ErrInvalid,
		},
		{
			name: "waive until not greater than now",
			setup: func(t *testing.T, g *gate.Gate) {
				base(t, g)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 11)
			},
			op:   func(g *gate.Gate) error { return g.Waive("s", "c", 15, 15) },
			want: gate.ErrInvalid,
		},
		{
			name:  "subscribe empty fields before subject check",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("zz", "c", 1, nil, 20) },
			want:  gate.ErrInvalid,
		},
		{
			name:  "subscribe duplicate fields",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("s", "c", 1, []string{"a", "a"}, 20) },
			want:  gate.ErrInvalid,
		},
		{
			name:  "clock regression before subject not found",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("zz", "c", 1, []string{"a"}, 5) },
			want:  gate.ErrClockRegression,
		},
		{
			name: "clock regression before already exists",
			setup: func(t *testing.T, g *gate.Gate) {
				base(t, g)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 11)
			},
			op:   func(g *gate.Gate) error { return g.Subscribe("s", "c", 1, []string{"a"}, 5) },
			want: gate.ErrClockRegression,
		},
		{
			name: "subject not found before already exists",
			setup: func(t *testing.T, g *gate.Gate) {
				base(t, g)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 11)
			},
			op:   func(g *gate.Gate) error { return g.Subscribe("zz", "c", 1, []string{"a"}, 20) },
			want: gate.ErrSubjectNotFound,
		},
		{
			name:  "subject already exists",
			setup: base,
			op: func(g *gate.Gate) error {
				_, err := g.CreateSubject("s", schema.Backward, v1, 20)
				return err
			},
			want: gate.ErrAlreadyExists,
		},
		{
			name: "subscriber already exists before bad pinned",
			setup: func(t *testing.T, g *gate.Gate) {
				base(t, g)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 11)
			},
			op:   func(g *gate.Gate) error { return g.Subscribe("s", "c", 99, []string{"a"}, 20) },
			want: gate.ErrAlreadyExists,
		},
		{
			name:  "subscriber not found on waive",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Waive("s", "c", 100, 20) },
			want:  gate.ErrSubscriberNotFound,
		},
		{
			name:  "subscriber not found on advance",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Advance("s", "c", 1, []string{"a"}, 20) },
			want:  gate.ErrSubscriberNotFound,
		},
		{
			name:  "subscriber not found on status",
			setup: base,
			op: func(g *gate.Gate) error {
				_, err := g.Status("s", "c")
				return err
			},
			want: gate.ErrSubscriberNotFound,
		},
		{
			name:  "pinned version not found",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("s", "c", 99, []string{"a"}, 20) },
			want:  gate.ErrVersionNotFound,
		},
		{
			name:  "pinned zero not found",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("s", "c", 0, []string{"a"}, 20) },
			want:  gate.ErrVersionNotFound,
		},
		{
			name:  "field not found",
			setup: base,
			op:    func(g *gate.Gate) error { return g.Subscribe("s", "c", 1, []string{"zz"}, 20) },
			want:  gate.ErrFieldNotFound,
		},
		{
			name:  "no change",
			setup: base,
			op: func(g *gate.Gate) error {
				_, err := g.Publish("s", []schema.Field{
					fld("a", schema.Int32, true, false),
					fld("b", schema.Int32, true, false),
				}, 20)
				return err
			},
			want: gate.ErrNoChange,
		},
		{
			name: "incompatible before blocked",
			setup: func(t *testing.T, g *gate.Gate) {
				mustCreate(t, g, "s", schema.Forward, v1, 0)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 1)
			},
			op: func(g *gate.Gate) error {
				_, err := g.Publish("s", []schema.Field{fld("a", schema.Int64, true, false)}, 2)
				return err
			},
			want: gate.ErrIncompatible,
		},
		{
			name: "blocked",
			setup: func(t *testing.T, g *gate.Gate) {
				mustCreate(t, g, "s", schema.Backward, v1, 0)
				mustSubscribe(t, g, "s", "c", 1, []string{"a"}, 1)
			},
			op: func(g *gate.Gate) error {
				_, err := g.Publish("s", []schema.Field{
					fld("a", schema.Int64, true, false),
					fld("b", schema.Int32, true, false),
				}, 2)
				return err
			},
			want: gate.ErrBlocked,
		},
		{
			name: "still broken on subscribe",
			setup: func(t *testing.T, g *gate.Gate) {
				mustCreate(t, g, "s", schema.Backward, v1, 0)
				if _, err := g.Publish("s", []schema.Field{fld("a", schema.Int32, true, false)}, 1); err != nil {
					t.Fatalf("Publish v2 = %v", err)
				}
			},
			op:   func(g *gate.Gate) error { return g.Subscribe("s", "c", 1, []string{"b"}, 2) },
			want: gate.ErrStillBroken,
		},
		{
			name:  "rejected op keeps clock and version",
			setup: base,
			op: func(g *gate.Gate) error {
				// ErrNoChange 被拒（now=100），不得推进时钟也不得占号。
				if _, err := g.Publish("s", v1, 100); !errors.Is(err, gate.ErrNoChange) {
					return err
				}
				ver, err := g.Publish("s", []schema.Field{fld("a", schema.Int32, true, false)}, 50)
				if err == nil && ver != 2 {
					return errors.New("rejected publish consumed a version number")
				}
				return err
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gate.New()
			if tt.setup != nil {
				tt.setup(t, g)
			}
			err := tt.op(g)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// 一次 Publish 的 compared 不超过 |N| + |latest| + 各非 Lagging 订阅者 fields 数之和，
// 与历史版本数（10/1000 两档）及其他主题的订阅者数无关。
func TestPublishComparedBound(t *testing.T) {
	a := []schema.Field{fld("id", schema.Int32, true, false)}
	b := []schema.Field{fld("id", schema.Int32, true, false), fld("x", schema.Int32, false, true)}
	for _, mode := range []schema.Mode{schema.Backward, schema.Full} {
		deltas := make([]int64, 0, 2)
		for _, versions := range []int{10, 1000} {
			t.Run(mode.String()+"/versions="+itoa(versions), func(t *testing.T) {
				g := gate.New()
				// 噪声：5 个其他主题，各 20 个订阅者。
				for i := 0; i < 5; i++ {
					name := "noise" + itoa(i)
					mustCreate(t, g, name, schema.Backward, []schema.Field{fld("n", schema.Int32, true, false)}, 0)
					for j := 0; j < 20; j++ {
						mustSubscribe(t, g, name, "nc"+itoa(j), 1, []string{"n"}, 0)
					}
				}
				mustCreate(t, g, "s", mode, a, 0)
				subFields := 0
				for j := 0; j < 3; j++ {
					mustSubscribe(t, g, "s", "c"+itoa(j), 1, []string{"id"}, 0)
					subFields++
				}
				now := int64(0)
				cur := a
				for v := 1; v < versions; v++ {
					now++
					if len(cur) == 1 {
						cur = b
					} else {
						cur = a
					}
					if _, err := g.Publish("s", cur, now); err != nil {
						t.Fatalf("Publish to v%d = %v", v+1, err)
					}
				}
				mustLatest(t, g, "s", versions)
				// 测量一次 Publish 的 compared 增量。
				now++
				lenLatest := len(cur)
				N := b
				if len(cur) != 1 {
					N = a
				}
				compat.ResetCompared()
				if _, err := g.Publish("s", N, now); err != nil {
					t.Fatalf("measured Publish = %v", err)
				}
				delta := compat.Compared()
				bound := int64(len(N) + lenLatest + subFields)
				if delta > bound {
					t.Fatalf("compared delta %d > bound %d (|N|=%d |latest|=%d subs=%d)",
						delta, bound, len(N), lenLatest, subFields)
				}
				t.Logf("mode=%v versions=%d delta=%d bound=%d", mode, versions, delta, bound)
				deltas = append(deltas, delta)
			})
		}
		if deltas[0] != deltas[1] {
			t.Fatalf("%v: compared delta depends on history size: %v", mode, deltas)
		}
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// 并发混合操作等价于某串行序（配合 go test -race 验证无数据竞争），
// 且版本号始终连续无洞。
func TestConcurrent(t *testing.T) {
	g := gate.New()
	mustCreate(t, g, "s", schema.Full, []schema.Field{fld("a", schema.Int32, true, false)}, 0)
	var wg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			consumer := "c" + itoa(id)
			for n := int64(1); n <= 30; n++ {
				_ = g.Subscribe("s", consumer, 1, []string{"a"}, n)
				_ = g.Waive("s", consumer, n+100, n)
				if _, err := g.Publish("s", []schema.Field{
					fld("a", schema.Int32, true, false),
					fld("f"+itoa(int(n)), schema.Int32, false, true),
				}, n); err == nil {
					successes.Add(1)
				}
				_ = g.Advance("s", consumer, int(n), []string{"a"}, n)
				_, _ = g.Status("s", consumer)
			}
		}(i)
	}
	wg.Wait()
	latest, err := g.Latest("s")
	if err != nil {
		t.Fatalf("Latest = %v", err)
	}
	if int64(latest) != successes.Load()+1 {
		t.Fatalf("latest = %d, successful publishes = %d: version numbers must be contiguous",
			latest, successes.Load())
	}
}
