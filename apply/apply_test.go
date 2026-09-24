package apply

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"

	"ontology/name"
	"ontology/plan"
)

func TestExecSuccessAndUndoEquality(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		init []string
		reqs []plan.Req
	}{
		{"empty", nil, nil},
		{"chain", []string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "c"}}},
		{"2-cycle", []string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "a"}}},
		{"3-cycle", []string{"a", "b", "c"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}},
		{"self loop", []string{"a", "b"}, []plan.Req{{"a", "a"}, {"b", "c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init)
			before := ns.Snapshot()
			path, _, err := Exec(ns, tc.reqs, dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("log missing: %v", err)
			}
			if name.Equal(ns.Snapshot(), before) && len(tc.reqs) > 0 &&
				tc.name != "2-cycle" && tc.name != "3-cycle" && tc.name != "empty" &&
				tc.name != "self loop" {
				// chain 的结果确实应改变
				t.Fatal("effective batch unexpectedly a no-op")
			}
			if !undoReverseForTest(t, ns, path) {
				t.Fatal("undo incomplete")
			}
			if !name.Equal(ns.Snapshot(), before) {
				t.Fatalf("after undo %v != before %v", ns.Snapshot(), before)
			}
		})
	}
}

func TestFailureRollback(t *testing.T) {
	dir := t.TempDir()
	init := []string{"a", "b", "c"}
	reqs := []plan.Req{{"a", "aa"}, {"b", "bb"}, {"c", "cc"},
		{"aa", "aaa"}, {"bb", "bbb"}}
	stepsLen := 5
	positions := []int{1, stepsLen/2 + 1, stepsLen}
	for _, k := range positions {
		t.Run("k="+itoaTest(k), func(t *testing.T) {
			ns := name.New(init)
			before := ns.Snapshot()
			path, _, err := Exec(ns, reqs, dir, FailAt(k))
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if path != "" {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatal("log file should be removed after rollback")
				}
			}
			if !name.Equal(ns.Snapshot(), before) {
				t.Fatalf("rollback mismatch: %v != %v", ns.Snapshot(), before)
			}
		})
	}
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// undoReverseForTest 用 apply 自身的日志编码做最小逆序撤销，
// 完整解析与截断分类在 undo 包测试。
func undoReverseForTest(t *testing.T, ns *name.Namespace, path string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var recs []record
	pos := HeaderLen
	for pos < len(data)-TrailerLen {
		if data[pos] != 0x01 {
			return false
		}
		n := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		var r record
		if err := json.Unmarshal(data[pos+3:pos+3+n], &r); err != nil {
			return false
		}
		recs = append(recs, r)
		pos += 3 + n
	}
	ns.Lock()
	defer ns.Unlock()
	for i := len(recs) - 1; i >= 0; i-- {
		if err := ns.RenameLocked(recs[i].To, recs[i].From, true); err != nil {
			return false
		}
	}
	return true
}
