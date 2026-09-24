package snap

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"

	"ontology/state"
)

func chVal(x int64) Change { return Change{Value: x} }

var chTomb = Change{Deleted: true}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// play 按行脚本驱动实例："set k v" / "del k" / "cp"。
func play(t *testing.T, c *Checkpointer, script string) {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "set":
			v, err := strconv.ParseInt(f[2], 10, 64)
			must(t, err)
			must(t, c.st.Set(f[1], v))
		case "del":
			c.st.Delete(f[1])
		case "cp":
			must(t, c.Checkpoint())
		}
	}
}

// TestSectionThreeScript 钉住第三节八行推导：四次检查点内容与最终恢复。
func TestSectionThreeScript(t *testing.T) {
	c := New(state.New(8))
	play(t, c, `
set a 1
set b 2
set c 3
cp
set a 10
del b
set d 4
cp
set a 1
set c 30
set e 5
cp
set c 3
cp`)
	h := c.History()
	want := []map[string]Change{
		{"a": chVal(1), "b": chVal(2), "c": chVal(3)},
		{"a": chVal(10), "b": chTomb, "d": chVal(4)},
		{"a": chVal(1), "c": chVal(30), "e": chVal(5)},
		{"c": chVal(3)},
	}
	for i := range want {
		if h[i].IsBase != (i == 0) || !maps.Equal(h[i].Data, want[i]) {
			t.Fatalf("checkpoint %d = %+v (base=%v), want %+v", i, h[i].Data, h[i].IsBase, want[i])
		}
	}
	rec, err := c.Recover()
	if wantRec := map[string]int64{"a": 1, "c": 3, "d": 4, "e": 5}; err != nil || !maps.Equal(rec, wantRec) {
		t.Fatalf("recover = %v err=%v, want %v", rec, err, wantRec)
	}
	if h[1].Data["b"] != chTomb {
		t.Fatal("(甲): b must be a tombstone in delta1")
	}
	if h[2].Data["a"] != chVal(1) {
		t.Fatal("(乙): a->1 must appear in delta2 even though it equals the base value")
	}
	if rec["c"] != 3 {
		t.Fatal("(丙): last-write-wins must yield c=3")
	}
}
func TestMergeOverwriteAndTombstone(t *testing.T) {
	cases := []struct {
		name  string
		play  string
		snaps []map[string]Change
		rec   map[string]int64
	}{
		{"tombstone deletes base key", "set x 1\ncp\ndel x\ncp",
			[]map[string]Change{{"x": chVal(1)}, {"x": chTomb}}, map[string]int64{}},
		{"added then deleted before first cp is omitted", "set z 7\ndel z\ncp",
			[]map[string]Change{{}}, map[string]int64{}},
		{"deleted then re-added at same value: empty delta", "set k 5\ncp\ndel k\nset k 5\ncp",
			[]map[string]Change{{"k": chVal(5)}, {}}, map[string]int64{"k": 5}},
		{"zero-valued new key is recorded", "set q 0\ncp",
			[]map[string]Change{{"q": chVal(0)}}, map[string]int64{"q": 0}},
		{"deleted in one delta, re-added in a later delta", "set a 1\ncp\ndel a\ncp\nset a 9\ncp",
			[]map[string]Change{{"a": chVal(1)}, {"a": chTomb}, {"a": chVal(9)}}, map[string]int64{"a": 9}},
		{"set same value is a no-op: empty delta", "set a 1\ncp\nset a 1\ncp",
			[]map[string]Change{{"a": chVal(1)}, {}}, map[string]int64{"a": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(state.New(16))
			play(t, c, tc.play)
			h := c.History()
			if len(h) != len(tc.snaps) {
				t.Fatalf("got %d checkpoints, want %d", len(h), len(tc.snaps))
			}
			for i := range tc.snaps {
				if h[i].IsBase != (i == 0) || !maps.Equal(h[i].Data, tc.snaps[i]) {
					t.Fatalf("cp %d = %v, want %v", i, h[i].Data, tc.snaps[i])
				}
			}
			rec, err := c.Recover()
			if err != nil || !maps.Equal(rec, tc.rec) {
				t.Fatalf("recover = %v err=%v, want %v", rec, err, tc.rec)
			}
		})
	}
}

// TestDeltaTraversalCount 多档 m：基线后只改 1 个键，delta 遍历数恒为 1，不随 m 线性增长。
func TestDeltaTraversalCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			st := state.New(m + 1)
			c := New(st)
			for i := range m {
				must(t, st.Set(fmt.Sprintf("k%d", i), int64(i)))
			}
			must(t, c.Checkpoint()) // 基线，清空脏集
			must(t, st.Set("k0", -1))
			must(t, c.Checkpoint())
			if c.scanCount != 1 { // 等于脏键个数；整表扫描会随 m 线性增长
				t.Fatalf("m=%d traversed %d keys, want exactly 1 dirty key", m, c.scanCount)
			}
		})
	}
}
