package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"ontology/dbuf"
	"ontology/view"
)

var failed bool

func judge(name string, cond bool) {
	tag := "OK"
	if !cond {
		tag = "FAIL"
		failed = true
	}
	fmt.Println(tag, name)
}

// ms 把计数视图格式化为确定序的 {a:3,b:1}。
func ms(m map[string]int64) string {
	if m == nil {
		return "nil"
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range ks {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s:%d", k, m[k])
	}
	sb.WriteByte('}')
	return sb.String()
}

// es 把事件列表格式化为 [(a,+1),(a,-2)]。
func es(evs []dbuf.Event) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, e := range evs {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "(%s,%+d)", e.Key, e.Delta)
	}
	sb.WriteByte(']')
	return sb.String()
}

type want struct {
	f, b string
	p    string
	reb  bool
}

func main() {
	// 第三节七步轨迹：逐步核对 F/B/pending/rebuilding。
	v := view.New([]dbuf.Event{{Key: "a", Delta: 1}, {Key: "a", Delta: 2}, {Key: "b", Delta: 1}})
	steps := []struct {
		name string
		run  func() error
		want want
	}{
		{"step1 StartRebuild", func() error { return v.StartRebuild() }, want{"{a:3,b:1}", "{}", "[]", true}},
		{"step2 RebuildStep(a,+1)", func() error { return v.RebuildStep(dbuf.Event{Key: "a", Delta: 1}) }, want{"{a:3,b:1}", "{a:1}", "[]", true}},
		{"step3 RebuildStep(a,+2)", func() error { return v.RebuildStep(dbuf.Event{Key: "a", Delta: 2}) }, want{"{a:3,b:1}", "{a:3}", "[]", true}},
		{"step4 Apply(a,+1)", func() error { return v.Apply(dbuf.Event{Key: "a", Delta: 1}) }, want{"{a:4,b:1}", "{a:3}", "[(a,+1)]", true}},
		{"step5 RebuildStep(b,+1)", func() error { return v.RebuildStep(dbuf.Event{Key: "b", Delta: 1}) }, want{"{a:4,b:1}", "{a:3,b:1}", "[(a,+1)]", true}},
		{"step6 Apply(a,-2)", func() error { return v.Apply(dbuf.Event{Key: "a", Delta: -2}) }, want{"{a:2,b:1}", "{a:3,b:1}", "[(a,+1),(a,-2)]", true}},
		{"step7 CommitSwitch", func() error { return v.CommitSwitch() }, want{"{a:2,b:1}", "nil", "[]", false}},
	}
	for _, s := range steps {
		err := s.run()
		b, p, reb := v.Snapshot()
		got := want{ms(v.View()), ms(b), es(p), reb}
		judge(fmt.Sprintf("%s F=%s B=%s pend=%s reb=%v", s.name, got.f, got.b, got.p, got.reb),
			err == nil && got == s.want)
	}
	if failed {
		os.Exit(1)
	}
}
