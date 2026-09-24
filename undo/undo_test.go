package undo

import (
	"errors"
	"os"
	"testing"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
)

func sampleLog(t *testing.T) []byte {
	t.Helper()
	steps := []plan.Step{
		{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"},
	}
	data, err := apply.WriteLog(steps)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseIntact(t *testing.T) {
	data := sampleLog(t)
	recs, total, err := Parse(data)
	if err != nil || total != 3 || len(recs) != 3 {
		t.Fatalf("recs=%d total=%d err=%v", len(recs), total, err)
	}
	want := []struct{ f, to string }{
		{"a", "b"}, {"b", "c"}, {"c", "d"},
	}
	for i, w := range want {
		if recs[i].From != w.f || recs[i].To != w.to || recs[i].Ord != i+1 {
			t.Fatalf("rec %d = %+v", i, recs[i])
		}
	}
}

func TestTruncationClassification(t *testing.T) {
	data := sampleLog(t)
	framesEnd := len(data) - apply.TrailerLen
	for cut := 1; cut < len(data); cut++ {
		trunc := data[:cut]
		_, _, err := Parse(trunc)
		var want error
		switch {
		case cut < apply.HeaderLen:
			want = ErrShortHeader
		case cut >= framesEnd:
			want = ErrCRC
		default:
			want = ErrBadRecord
		}
		if !errors.Is(err, want) {
			t.Fatalf("cut %d: err=%v want %v", cut, err, want)
		}
	}
	classSeen := map[error]int{}
	for cut := 1; cut < len(data); cut++ {
		_, _, err := Parse(data[:cut])
		classSeen[err]++
	}
	for _, e := range []error{ErrShortHeader, ErrBadRecord, ErrCRC} {
		if classSeen[e] == 0 {
			t.Fatalf("error class never produced: %v", e)
		}
	}
}

func TestTruncatedUndoPrefix(t *testing.T) {
	// 先真实执行整批（独立改名），再用“第一条记录完整、其后截断”的日志撤销。
	ns := name.New([]string{"a", "b", "c"})
	path, _, err := apply.Exec(ns,
		[]plan.Req{{"a", "ra"}, {"b", "rb"}, {"c", "rc"}}, t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	executed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 用该真实日志的第一条帧边界构造截断日志。
	epos := apply.HeaderLen
	en := int(executed[epos+1])<<8 | int(executed[epos+2])
	cutAt := epos + 3 + en + 1
	res, err := UndoBytes(ns, executed[:cutAt])
	if !errors.Is(err, ErrBadRecord) {
		t.Fatalf("err=%v want ErrBadRecord", err)
	}
	if res.Recovered != 1 || res.Total != 3 || len(res.Missing) != 2 ||
		res.Missing[0] != 2 || res.Missing[1] != 3 {
		t.Fatalf("result=%+v", res)
	}
}

func TestIdempotentUndo(t *testing.T) {
	dir := t.TempDir()
	ns := name.New([]string{"a", "b", "c"})
	before := ns.Snapshot()
	path, _, err := apply.Exec(ns,
		[]plan.Req{{"a", "x"}, {"b", "y"}, {"c", "z"}}, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := UndoFile(ns, path)
	if err != nil || !r1.Full || !name.Equal(ns.Snapshot(), before) {
		t.Fatalf("first undo: %+v %v %v", r1, err, ns.Snapshot())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("log should be renamed to .done")
	}
	r2, err := UndoFile(ns, path)
	if err != nil || !r2.Full || !name.Equal(ns.Snapshot(), before) {
		t.Fatalf("second undo not idempotent: %+v %v", r2, err)
	}
}

func TestFullUndoRestores(t *testing.T) {
	cases := []struct {
		name string
		init []string
		reqs []plan.Req
	}{
		{"chain", []string{"a", "b"}, []plan.Req{{"a", "b"}, {"b", "c"}}},
		{"3-cycle", []string{"a", "b", "c"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ns := name.New(tc.init)
			before := ns.Snapshot()
			path, _, err := apply.Exec(ns, tc.reqs, dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			res, err := UndoFile(ns, path)
			if err != nil || !res.Full {
				t.Fatalf("undo: %+v %v", res, err)
			}
			if !name.Equal(ns.Snapshot(), before) {
				t.Fatalf("restored %v != before %v", ns.Snapshot(), before)
			}
		})
	}
}
