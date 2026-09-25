// Command demo 逐条打印 ontology 校验钩子语义的 OK/FAIL。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"ontology/api"
	"ontology/hook"
	"ontology/schedule"
	"ontology/snapshot"
)

type checkResult struct {
	name string
	ok   bool
}

func check(name string, ok bool) checkResult { return checkResult{name, ok} }

func passCheck(snapshot.Snapshot) (bool, string) { return true, "" }

func failCheck(reason string) hook.Check {
	return func(snapshot.Snapshot) (bool, string) { return false, reason }
}

// endAfterStart 是跨字段不变量：end 必须大于 start。
func endAfterStart(s snapshot.Snapshot) (bool, string) {
	start, _ := s.Get("start")
	end, _ := s.Get("end")
	return end.(int) > start.(int), fmt.Sprintf("end(%v) 必须大于 start(%v)", end, start)
}

func hk(name string, phase hook.Phase, c hook.Check) hook.Hook {
	return hook.Hook{Name: name, AppliesTo: "Task", Phase: phase, Check: c}
}

func regOf(hooks ...hook.Hook) *hook.Registry {
	r := hook.NewRegistry()
	for _, h := range hooks {
		r.Register(h)
	}
	return r
}

func checks() []checkResult {
	var out []checkResult

	// 判定 1：同属性不同插入顺序，快照编码逐字节相同。
	sa := snapshot.Freeze("Task", map[string]any{"start": 1, "end": 9, "owner": "x"})
	sb := snapshot.Freeze("Task", map[string]any{"owner": "x", "end": 9, "start": 1})
	// 判定 2：冻结后源 map 被改、快照被读多次，字节始终不变。
	attrs := map[string]any{"name": "t1", "level": 3}
	s := snapshot.Freeze("Task", attrs)
	before := s.Bytes()
	attrs["name"] = "mutated"
	attrs["injected"] = true
	_, _ = s.Get("name")
	_ = s.Keys()
	out = append(out,
		check("快照按属性名排序、字节级确定", bytes.Equal(sa.Bytes(), sb.Bytes())),
		check("冻结快照不可变（源对象改动/多次读取均不影响）", bytes.Equal(before, s.Bytes())))
	// 判定 3：Match 只返回 appliesTo 相同的钩子。
	reg := regOf(hk("task-pre", hook.Pre, passCheck), hk("task-post", hook.Post, passCheck))
	reg.Register(hook.Hook{Name: "user-pre", AppliesTo: "User", Phase: hook.Pre, Check: passCheck})
	matched := reg.Match("Task")
	matchOK := len(matched) == 2
	for _, h := range matched {
		matchOK = matchOK && h.AppliesTo == "Task"
	}
	// 判定 4：匹配访问数不随已注册钩子总数增长（100/10000 两档）。
	visits := map[int]int{}
	for _, total := range []int{100, 10000} {
		r := regOf(hk("target", hook.Pre, passCheck))
		for i := 0; i < total-1; i++ {
			r.Register(hook.Hook{Name: fmt.Sprintf("noise-%d", i), AppliesTo: "Other", Phase: hook.Pre, Check: passCheck})
		}
		r.Match("Task")
		visits[total] = r.MatchVisits()
	}
	out = append(out,
		check("钩子按对象类型匹配（appliesTo）", matchOK),
		check(fmt.Sprintf("匹配访问数不随钩子总数增长（100→%d, 10000→%d）", visits[100], visits[10000]),
			visits[100] == visits[10000]))
	// 判定 5+6：任意注册顺序执行序列逐字节相同，且 pre 组整体先于 post 组。
	orders := [][]string{{"p1", "q1", "p2", "q2", "p3"}, {"q2", "p3", "q1", "p1", "p2"}, {"p2", "q1", "p1", "q2", "p3"}}
	var firstSeq string
	same, preFirst := true, true
	for _, order := range orders {
		r := hook.NewRegistry()
		for _, n := range order {
			phase := hook.Pre
			if n[0] == 'q' {
				phase = hook.Post
			}
			r.Register(hk(n, phase, passCheck))
		}
		plan := schedule.Plan(r.Match("Task"))
		seq := ""
		seenPost := false
		for _, h := range plan {
			seq += h.Name + ";"
			seenPost = seenPost || h.Phase == hook.Post
			preFirst = preFirst && (!seenPost || h.Phase == hook.Post)
		}
		if firstSeq == "" {
			firstSeq = seq
		}
		same = same && seq == firstSeq
	}
	out = append(out,
		check("执行顺序确定（任意注册顺序序列相同）", same),
		check("pre 组整体先于 post 组", preFirst))
	obj := api.Object{Type: "Task", Attrs: map[string]any{"start": 5, "end": 9}}
	setEnd := func(v int) api.Change { return api.Change{Set: map[string]any{"end": v}} }
	// 判定 7：跨字段不变量挂 post——看到新值，违规被拒、合法放行。
	v := api.NewValidator(regOf(hk("end-after-start", hook.Post, endAfterStart)))
	bad, good := v.Validate(obj, setEnd(3)), v.Validate(obj, setEnd(10))
	// 判定 8：同一不变量挂 pre——只看旧值（5<9 合法），违规变更被静默放过（误判演示）。
	misjudged := api.NewValidator(regOf(hk("end-after-start", hook.Pre, endAfterStart))).Validate(obj, setEnd(3))
	out = append(out,
		check("跨字段不变量挂 post：违规被拒、合法放行", errors.Is(bad, api.ErrPostFailed) && good == nil),
		check("同一不变量挂 pre 会拿旧值误判（违规变更被放过）", misjudged == nil))
	// 判定 9：只读契约——任意注册顺序结果相同，所有钩子看到同一份冻结快照。
	var allSnaps [][]byte
	runWithOrder := func(names []string) string {
		r := hook.NewRegistry()
		for _, n := range names {
			name := n
			r.Register(hk(name, hook.Pre, func(s snapshot.Snapshot) (bool, string) {
				allSnaps = append(allSnaps, s.Bytes())
				return name == "ok1", name + " rejected"
			}))
		}
		err := api.NewValidator(r).Validate(obj, setEnd(10))
		if err == nil {
			return ""
		}
		return err.Error()
	}
	r1, r2 := runWithOrder([]string{"f1", "ok1", "f2"}), runWithOrder([]string{"f2", "f1", "ok1"})
	snapsEqual := len(allSnaps) > 0
	for _, b := range allSnaps[1:] {
		snapsEqual = snapsEqual && bytes.Equal(allSnaps[0], b)
	}
	out = append(out, check("只读契约：任意顺序结果相同、所有钩子看到同一快照", r1 != "" && r1 == r2 && snapsEqual))
	// 判定 10：pre 失败阻止 post——单字段前置条件不满足，变更不发生，post 零执行。
	postRan := 0
	blocked := api.NewValidator(regOf(
		hk("require-open-status", hook.Pre, func(s snapshot.Snapshot) (bool, string) {
			st, _ := s.Get("status")
			return st == "open", "status 必须为 open"
		}),
		hk("post-probe", hook.Post, func(snapshot.Snapshot) (bool, string) { postRan++; return true, "" }),
	)).Validate(api.Object{Type: "Task", Attrs: map[string]any{"status": "closed"}},
		api.Change{Set: map[string]any{"level": 5}})
	out = append(out, check("pre 失败阻止 post 运行", errors.Is(blocked, api.ErrPreFailed) && postRan == 0))
	// 判定 11：同阶段多失败聚合——一次报全部，且不误标 post 哨兵。
	aggErr := api.NewValidator(regOf(
		hk("agg-a", hook.Pre, failCheck("a 失败")), hk("agg-b", hook.Pre, failCheck("b 失败")),
		hk("agg-c", hook.Post, failCheck("c 不应运行")))).Validate(obj, setEnd(10))
	var aggVE *api.ValidationError
	out = append(out, check("同阶段多失败聚合（一次报全部）",
		errors.As(aggErr, &aggVE) && len(aggVE.Failures) == 2 &&
			errors.Is(aggErr, api.ErrPreFailed) && !errors.Is(aggErr, api.ErrPostFailed)))
	// 判定 12：钩子 panic 恢复为 ErrHookInternal，仍带阶段哨兵。
	panicErr := api.NewValidator(regOf(
		hk("boom", hook.Post, func(snapshot.Snapshot) (bool, string) { panic("boom") }))).Validate(obj, setEnd(10))
	out = append(out, check("钩子内部错误可用 errors.Is(ErrHookInternal) 区分",
		errors.Is(panicErr, api.ErrHookInternal) && errors.Is(panicErr, api.ErrPostFailed)))
	// 判定 13：参数校验——空类型/空变更集，不进入任何钩子。
	errNoType := v.Validate(api.Object{Type: "", Attrs: map[string]any{}}, api.Change{Set: map[string]any{"a": 1}})
	errNoChange := v.Validate(obj, api.Change{})
	out = append(out, check("参数校验（空类型/空变更集）",
		errors.Is(errNoType, api.ErrInvalidArgument) && errors.Is(errNoChange, api.ErrInvalidArgument)))

	return out
}

func main() {
	failed := 0
	for _, c := range checks() {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	if failed > 0 {
		fmt.Printf("%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
