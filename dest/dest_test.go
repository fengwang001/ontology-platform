package dest_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ontology/dest"
)

func TestNewLimit(t *testing.T) {
	for _, l := range []int{-1, 0, 65537, 1 << 20} {
		if _, err := dest.New(l); !errors.Is(err, dest.ErrInvalidParam) {
			t.Errorf("New(%d): err = %v, want ErrInvalidParam", l, err)
		}
	}
	for _, l := range []int{1, 4, 65536} {
		if _, err := dest.New(l); err != nil {
			t.Errorf("New(%d): %v", l, err)
		}
	}
}

// dop 描述对 dest 的一次操作及期望返回值。
type dop struct {
	del     bool
	id      string
	body    string
	ver     uint64
	wantApp bool
	wantInc bool
}

func runOps(t *testing.T, d *dest.Dest, ops []dop) {
	t.Helper()
	for i, o := range ops {
		var app, inc bool
		if o.del {
			app = d.Delete(o.id, o.ver)
		} else {
			app, inc = d.Index(o.id, []byte(o.body), o.ver)
		}
		if app != o.wantApp || inc != o.wantInc {
			t.Errorf("op#%d %+v: got (applied=%v, incompatible=%v), want (%v, %v)",
				i, o, app, inc, o.wantApp, o.wantInc)
		}
	}
}

func TestVersionRules(t *testing.T) {
	cases := []struct {
		name          string
		limit         int
		ops           []dop
		wantConflicts uint64
		wantRecs      map[string]dest.Record
		wantFailures  []string
	}{
		{
			name:  "ver 恰等算冲突（含对墓碑）",
			limit: 4,
			ops: []dop{
				{id: "a", body: "x", ver: 5, wantApp: true},
				{id: "a", body: "y", ver: 5},                // 恰等 → 冲突
				{del: true, id: "a", ver: 5},                // 恰等 → 冲突
				{del: true, id: "a", ver: 6, wantApp: true}, // 墓碑 a@6
				{id: "a", body: "z", ver: 6},                // 对墓碑恰等 → 冲突
				{id: "a", body: "z", ver: 4},                // 小于墓碑 → 冲突
				{id: "a", body: "z", ver: 7, wantApp: true}, // 更大 → 复活
			},
			wantConflicts: 4,
			wantRecs: map[string]dest.Record{
				"a": {Ver: 7, Body: []byte("z")},
			},
		},
		{
			name:  "无记录 Delete 必须留墓碑",
			limit: 4,
			ops: []dop{
				{del: true, id: "z", ver: 3, wantApp: true}, // 无记录也留墓碑
				{id: "z", body: "x", ver: 3},                // 恰等 → 冲突
				{id: "z", body: "x", ver: 2},                // 更小 → 冲突
				{id: "z", body: "x", ver: 4, wantApp: true}, // 更大 → 应用
			},
			wantConflicts: 2,
			wantRecs: map[string]dest.Record{
				"z": {Ver: 4, Body: []byte("x")},
			},
		},
		{
			name:  "不兼容写入抹掉旧值",
			limit: 4,
			ops: []dop{
				{id: "a", body: "ok", ver: 1, wantApp: true},
				{id: "a", body: "toolong", ver: 2, wantApp: true, wantInc: true}, // 旧值消失
			},
			wantRecs: map[string]dest.Record{
				"a": {Ver: 2, Tombstone: true, Incompatible: true},
			},
			wantFailures: []string{"a"},
		},
		{
			name:  "不兼容但版本不够只计冲突",
			limit: 4,
			ops: []dop{
				{id: "a", body: "ok", ver: 5, wantApp: true},
				{id: "a", body: "toolong", ver: 5}, // 恰等 → 只计冲突
				{id: "a", body: "toolong", ver: 3}, // 更小 → 只计冲突
			},
			wantConflicts: 2,
			wantRecs: map[string]dest.Record{
				"a": {Ver: 5, Body: []byte("ok")}, // 旧值保留
			},
		},
		{
			name:  "失败集合被更大版本写入自动移出",
			limit: 4,
			ops: []dop{
				{id: "a", body: "toolong", ver: 1, wantApp: true, wantInc: true},
				{id: "b", body: "alsolong", ver: 1, wantApp: true, wantInc: true},
				{id: "a", body: "ok", ver: 2, wantApp: true}, // a 移出失败集合
			},
			wantRecs: map[string]dest.Record{
				"a": {Ver: 2, Body: []byte("ok")},
				"b": {Ver: 1, Tombstone: true, Incompatible: true},
			},
			wantFailures: []string{"b"},
		},
		{
			name:  "删除覆盖不兼容墓碑即移出失败集合",
			limit: 4,
			ops: []dop{
				{id: "a", body: "toolong", ver: 1, wantApp: true, wantInc: true},
				{del: true, id: "a", ver: 2, wantApp: true}, // 普通墓碑覆盖
			},
			wantRecs: map[string]dest.Record{
				"a": {Ver: 2, Tombstone: true},
			},
		},
		{
			name:  "无记录的不兼容写入直接成墓碑",
			limit: 4,
			ops: []dop{
				{id: "q", body: "toolong", ver: 9, wantApp: true, wantInc: true},
			},
			wantRecs: map[string]dest.Record{
				"q": {Ver: 9, Tombstone: true, Incompatible: true},
			},
			wantFailures: []string{"q"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := dest.New(tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			runOps(t, d, tc.ops)
			if got := d.Conflicts(); got != tc.wantConflicts {
				t.Errorf("Conflicts = %d, want %d", got, tc.wantConflicts)
			}
			if got := d.All(); !reflect.DeepEqual(got, tc.wantRecs) {
				t.Errorf("All = %+v, want %+v", got, tc.wantRecs)
			}
			var failIDs []string
			for id := range d.Failures() {
				failIDs = append(failIDs, id)
			}
			if len(failIDs) == 0 {
				failIDs = nil
			}
			if !reflect.DeepEqual(failIDs, tc.wantFailures) &&
				!(len(failIDs) == 0 && len(tc.wantFailures) == 0) {
				t.Errorf("Failures = %v, want %v", failIDs, tc.wantFailures)
			}
		})
	}
}

func TestClearTombstones(t *testing.T) {
	d, _ := dest.New(4)
	runOps(t, d, []dop{
		{id: "live", body: "ok", ver: 1, wantApp: true},
		{del: true, id: "gone", ver: 2, wantApp: true},
		{id: "bad", body: "toolong", ver: 3, wantApp: true, wantInc: true},
	})
	d.ClearTombstones()
	got := d.All()
	want := map[string]dest.Record{"live": {Ver: 1, Body: []byte("ok")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("清除墓碑后 = %+v, want %+v", got, want)
	}
	if n := len(d.Failures()); n != 0 {
		t.Fatalf("失败集合应随墓碑清空, got %d", n)
	}
}

func TestReset(t *testing.T) {
	d, _ := dest.New(4)
	runOps(t, d, []dop{
		{id: "a", body: "ok", ver: 1, wantApp: true},
		{id: "a", body: "ok", ver: 1}, // 冲突
	})
	if d.Empty() {
		t.Fatal("写入后不应为空")
	}
	d.Reset()
	if !d.Empty() {
		t.Fatal("Reset 后应为空")
	}
	if got := d.Conflicts(); got != 0 {
		t.Fatalf("Reset 后冲突数 = %d, want 0", got)
	}
}

func TestBodyLimitBoundary(t *testing.T) {
	d, _ := dest.New(4)
	runOps(t, d, []dop{
		{id: "a", body: strings.Repeat("x", 4), ver: 1, wantApp: true},                // 恰等 L，兼容
		{id: "b", body: strings.Repeat("x", 5), ver: 1, wantApp: true, wantInc: true}, // L+1，不兼容
	})
}
