// Command demo 演示可撤销批量重命名与冲突解析器的全部判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

var total, failures int

func check(name string, ok bool) {
	total++
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	failures++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	check("name: 空串与分隔符合法、NUL 非法", name.Valid("") && name.Valid("a/b") && !name.Valid("a\x00b"))

	var steps [][2]string
	cycle.Break([]string{"a", "b", "c"}, "#rename-tmp-0", func(o, n string) { steps = append(steps, [2]string{o, n}) })
	want := [][2]string{{"a", "#rename-tmp-0"}, {"c", "a"}, {"b", "c"}, {"#rename-tmp-0", "b"}}
	ok := len(steps) == len(want)
	for i := range want {
		if ok && steps[i] != want[i] {
			ok = false
		}
	}
	check("cycle: 三元环破环为 4 步且仅用 1 个临时名", ok)

	// plan：链式顺序与无覆盖。
	s := name.Must("a", "b")
	p, err := plan.Compile(s, []plan.Request{R("a", "b"), R("b", "c")})
	check("plan: {a→b,b→c} 顺序为 b→c,a→b 且全程无覆盖",
		err == nil && fmt.Sprint(p.Steps) == "[{b c} {a b}]" && runClean(s, p.Steps))

	// plan：三元环借一个临时名，每步目标名都不存在。
	s3 := name.Must("a", "b", "c")
	p3, err := plan.Compile(s3, []plan.Request{R("a", "b"), R("b", "c"), R("c", "a")})
	check("plan: 三元环借 1 个临时名且每步目标名不存在",
		err == nil && temps(p3.Steps) == 1 && runClean(s3, p3.Steps))

	// plan：临时名被预先占用仍能成功。
	pre := []string{"a", "b"}
	for i := 0; i < 8; i++ {
		pre = append(pre, fmt.Sprintf("#rename-tmp-%d", i))
	}
	sp := name.Must(pre...)
	pp, err := plan.Compile(sp, []plan.Request{R("a", "b"), R("b", "a")})
	check("plan: 临时名被预先占用仍能成功", err == nil && runClean(sp, pp.Steps))

	// plan：四类冲突各一例，检测后命名空间逐元素未变。
	conf := []struct {
		space []string
		reqs  []plan.Request
		want  error
	}{
		{[]string{"a", "b"}, []plan.Request{R("a", "b")}, plan.ErrTargetExists},
		{[]string{"a", "b"}, []plan.Request{R("a", "c"), R("b", "c")}, plan.ErrDuplicateTarget},
		{[]string{"a"}, []plan.Request{R("a", "b"), R("a", "c")}, plan.ErrDuplicateSource},
		{[]string{"a"}, []plan.Request{R("zz", "b")}, plan.ErrSourceMissing},
	}
	for _, c := range conf {
		cs := name.Must(c.space...)
		before := cs.Snapshot()
		_, err := plan.Compile(cs, c.reqs)
		check("plan: 冲突 "+errString(err)+" 且命名空间未变",
			errors.Is(err, c.want) && fmt.Sprint(cs.Snapshot()) == fmt.Sprint(before))
	}

	// plan：打乱构造顺序 20 次，步骤序列逐元素相同。
	det := true
	var first []plan.Step
	for seed := 0; seed < 20; seed++ {
		reqs := []plan.Request{R("a", "b"), R("b", "c"), R("d", "e"), R("f", "g")}
		for i := range reqs { // 简单确定性打乱
			j := (i + seed) % len(reqs)
			reqs[i], reqs[j] = reqs[j], reqs[i]
		}
		dp, err := plan.Compile(name.Must("a", "b", "d", "f"), reqs)
		if err != nil {
			det = false
			break
		}
		if first == nil {
			first = dp.Steps
		} else if fmt.Sprint(dp.Steps) != fmt.Sprint(first) {
			det = false
			break
		}
	}
	check("plan: 打乱构造顺序 20 次步骤序列一致", det)

	// plan：临时名数等于环数（10 个互不相交的环）。
	var cycReqs []plan.Request
	var cycNames []string
	for i := 0; i < 10; i++ {
		a, b := fmt.Sprintf("c%da", i), fmt.Sprintf("c%db", i)
		cycNames = append(cycNames, a, b)
		cycReqs = append(cycReqs, R(a, b), R(b, a))
	}
	cp, err := plan.Compile(name.Must(cycNames...), cycReqs)
	check("plan: 10 个环恰好用 10 个临时名", err == nil && temps(cp.Steps) == 10)

	demoSteps := []plan.Step{{Old: "a", New: "x"}, {Old: "b", New: "y"}, {Old: "c", New: "z"}}

	// apply：第 k 步注入失败后完整回滚（首步 / 中间 / 最后一步）。
	for _, k := range []int{0, 1, 2} {
		ks := name.Must("a", "b", "c")
		before := fmt.Sprint(ks.Snapshot())
		err := (&apply.Executor{FailAt: k}).Do(ks, demoSteps)
		check(fmt.Sprintf("apply: 第 %d 步失败后完整回滚", k),
			errors.Is(err, apply.ErrInjected) && fmt.Sprint(ks.Snapshot()) == before)
	}

	// apply+undo：执行成功后撤销，命名空间逐元素相同。
	dir, _ := os.MkdirTemp("", "onto-demo")
	us := name.Must("a", "b", "c")
	before := fmt.Sprint(us.Snapshot())
	log := filepath.Join(dir, "op.log")
	_ = (&apply.Executor{LogPath: log, FailAt: -1}).Do(us, demoSteps)
	_, uerr := undo.Undo(us, log)
	check("undo: 撤销后命名空间逐元素相同", uerr == nil && fmt.Sprint(us.Snapshot()) == before)

	// undo：三类截断分类各一例。
	full := apply.Encode(demoSteps)
	cutAt := func(c int) error {
		ts := name.Must("a", "b", "c")
		lg := filepath.Join(dir, "t.log")
		_ = (&apply.Executor{LogPath: lg, FailAt: -1}).Do(ts, demoSteps)
		_ = os.WriteFile(lg, full[:c], 0o600)
		_ = os.Remove(lg + ".done")
		_, err := undo.Undo(ts, lg)
		return err
	}
	check("undo: 截断于头部 → 头部不完整", errors.Is(cutAt(8), undo.ErrHeaderIncomplete))
	check("undo: 截断于记录区 → 记录不完整", errors.Is(cutAt(20), undo.ErrRecordIncomplete))
	check("undo: 截断于 CRC → CRC 不匹配", errors.Is(cutAt(len(full)-2), undo.ErrCRCMismatch))

	fmt.Printf("TOTAL %d checks, %d failed\n", total, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// runClean 逐步执行并校验每一步的目标名都不存在，返回是否全程无覆盖。
func runClean(s *name.Space, steps []plan.Step) bool {
	s.Lock()
	defer s.Unlock()
	for _, st := range steps {
		if s.HasLocked(st.New) {
			return false
		}
		if err := s.RenameLocked(st.Old, st.New); err != nil {
			return false
		}
	}
	return true
}

func temps(steps []plan.Step) int {
	n := 0
	for _, st := range steps {
		if cycle.IsTemp(st.Old) {
			n++
		}
	}
	return n
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()[:24]
}

// R 是构造 plan.Request 的简写。
func R(o, n string) plan.Request { return plan.Request{Old: o, New: n} }
