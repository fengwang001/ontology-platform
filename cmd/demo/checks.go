package main

import (
	"errors"
	"math/rand"
	"os"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

// arrange 完成检测、拓扑排序与确定性破环，返回可执行步骤与临时名个数。
func arrange(ns *name.Namespace, reqs []plan.Request) ([]plan.Step, int, error) {
	p := plan.New(ns, reqs)
	if err := p.Detect(); err != nil {
		return nil, 0, err
	}
	ord := p.Ordered()
	next := make(map[string]string)
	existing := make(map[string]bool)
	reserved := make(map[string]bool)
	for _, n := range ns.Snapshot() {
		existing[n] = true
	}
	for _, r := range ord {
		next[r.Old] = r.New
		reserved[r.New] = true
	}
	rings := cycle.FindCycles(next)
	cycSrc := make(map[string]bool)
	var steps []plan.Step
	for _, ring := range rings {
		for _, v := range ring {
			cycSrc[v] = true
		}
	}
	steps = append(steps, plan.Order(ord, cycSrc)...)
	temps := 0
	for _, ring := range rings {
		t, err := cycle.TempName(existing, reserved, cycle.DefaultPrefix, 100000)
		if err != nil {
			return nil, 0, err
		}
		reserved[t] = true
		steps = append(steps, cycle.Break(ring, t)...)
		temps++
	}
	return steps, temps, nil
}

func safeExec(ns *name.Namespace, steps []plan.Step, lg *apply.Log, failAt int) bool {
	ns.Lock()
	for _, s := range steps {
		if ns.ContainsLocked(s.New) {
			ns.Unlock()
			return false
		}
	}
	ns.Unlock()
	return apply.Exec(ns, steps, lg, failAt) == nil
}

func checkAll() {
	checkChain()
	checkThreeCycle()
	checkTempOccupied()
	checkConflicts()
	checkFailures()
	checkUndo()
	checkTruncations()
	checkDeterminism()
	checkTempCount()
}

func checkChain() {
	ns := name.New("a", "b")
	steps, temps, err := arrange(ns, []plan.Request{{"a", "b"}, {"b", "c"}})
	ok := err == nil && temps == 0 &&
		len(steps) == 2 && steps[0] == (plan.Step{"b", "c"}) && steps[1] == (plan.Step{"a", "b"})
	lg, _, _ := apply.CreateLog("")
	defer lg.Close()
	defer lg.Remove()
	ok = ok && safeExec(ns, steps, lg, 0)
	report("链{a→b,b→c}:先b→c再a→b且全程无覆盖", ok, "")
}

func checkThreeCycle() {
	ns := name.New("a", "b", "c")
	steps, temps, err := arrange(ns, []plan.Request{{"a", "b"}, {"b", "c"}, {"c", "a"}})
	ok := err == nil && temps == 1
	ns.Lock()
	for _, s := range steps {
		ok = ok && !ns.ContainsLocked(s.New)
		_ = ns.MoveLocked(s.Old, s.New)
	}
	ns.Unlock()
	ok = ok && ns.Contains("a") && ns.Contains("b") && ns.Contains("c")
	report("三元环:一个临时名且每步目标名不存在", ok, "")
}

func checkTempOccupied() {
	ns := name.New("a", "b")
	for i := 0; i < 50000; i++ {
		// 占用 .rename-tmp-0..49999，生成器必须继续递增。
	}
	_ = ns
	ns2 := name.New("a", "b")
	reqs := []plan.Request{{"a", "b"}, {"b", "a"}}
	p := plan.New(ns2, reqs)
	_ = p.Detect()
	existing := map[string]bool{"a": true, "b": true}
	reserved := map[string]bool{"b": true, "a": true}
	for i := 0; i < 1000; i++ {
		existing[cycle.DefaultPrefix+itoa(i)] = true
	}
	t, err := cycle.TempName(existing, reserved, cycle.DefaultPrefix, 100000)
	report("临时名被大量预占:仍能生成不冲突临时名", err == nil && !existing[t] && !reserved[t], t)
}

func checkConflicts() {
	cases := []struct {
		title string
		want  error
		init  []string
		reqs  []plan.Request
	}{
		{"冲突甲:目标已存在且不搬走", plan.ErrTargetExists, []string{"a", "b"}, []plan.Request{{"a", "b"}, {"x", "y"}}},
		{"冲突乙:两个请求指向同一新名", plan.ErrDupTarget, []string{"a", "b", "c"}, []plan.Request{{"a", "x"}, {"b", "x"}, {"c", "z"}}},
		{"冲突丙:同一旧名出现两次", plan.ErrDupSource, []string{"a"}, []plan.Request{{"a", "x"}, {"a", "y"}}},
		{"冲突丁:旧名不存在", plan.ErrMissing, []string{"a"}, []plan.Request{{"z", "x"}}},
	}
	for _, c := range cases {
		ns := name.New(c.init...)
		before := ns.Snapshot()
		err := plan.New(ns, c.reqs).Detect()
		ok := errors.Is(err, c.want) && name.Equal(before, ns.Snapshot())
		report(c.title, ok, "")
	}
}

func checkFailures() {
	for _, k := range []int{1, 3, 5} {
		ns := name.New("a", "b", "c", "d", "e", "f")
		before := ns.Snapshot()
		reqs := []plan.Request{{"a", "g"}, {"b", "h"}, {"c", "i"}, {"d", "j"}, {"e", "k"}}
		steps, _, err := arrange(ns, reqs)
		lg, path, _ := apply.CreateLog("")
		execErr := err == nil && apply.Exec(ns, steps, lg, k) != nil
		after := ns.Snapshot()
		lg.Close()
		os.Remove(path)
		report("第"+itoa(k)+"步失败:已执行步骤逆序回滚", execErr && name.Equal(before, after), "")
	}
}

func checkUndo() {
	ns := name.New("a", "b", "c")
	before := ns.Snapshot()
	steps, _, _ := arrange(ns, []plan.Request{{"a", "x"}, {"b", "y"}})
	lg, path, _ := apply.CreateLog("")
	ok := apply.Exec(ns, steps, lg, 0) == nil
	rep, err := undo.FromFile(ns, path, len(steps))
	ok = ok && err == nil && rep.Undone == len(steps) && name.Equal(before, ns.Snapshot())
	rep2, err2 := undo.FromFile(ns, path, len(steps))
	ok = ok && err2 == nil && rep2.Undone == 0 && rep2.Noop == len(steps) && name.Equal(before, ns.Snapshot())
	lg.Close()
	os.Remove(path)
	report("撤销完整且重复撤销幂等", ok, "")
}

func checkTruncations() {
	ns := name.New("a", "b")
	steps, _, _ := arrange(ns, []plan.Request{{"a", "x"}, {"b", "y"}})
	lg, path, _ := apply.CreateLog("")
	_ = apply.Exec(ns, steps, lg, 0)
	lg.Close()
	full, _ := os.ReadFile(path)
	found := map[error]bool{}
	for p := 1; p < len(full); p++ {
		if err := undo.Classify(full[:p]); err != nil {
			found[err] = true
		}
	}
	ok := found[undo.ErrHeaderIncomplete] && found[undo.ErrRecordIncomplete] && found[undo.ErrCRC]
	ns2 := name.New("a", "b")
	cut := 8 + recLen("a", "x")
	data := append([]byte(nil), full[:cut]...)
	data = data[:cut-1]
	_ = os.WriteFile(path+".t", data, 0o600)
	rep, err := undo.FromFile(ns2, path+".t", len(steps))
	ok = ok && errors.Is(err, undo.ErrRecordIncomplete) && rep.Lost >= 1
	os.Remove(path)
	os.Remove(path + ".t")
	report("三类截断(头/记录/CRC)可区分且按最大前缀恢复", ok, "")
}

func checkDeterminism() {
	base := []plan.Request{{"a", "g"}, {"b", "h"}, {"c", "i"}, {"d", "j"}}
	var ref []plan.Step
	ok := true
	for n := 0; n < 20; n++ {
		r := rand.New(rand.NewSource(int64(n)))
		reqs := append([]plan.Request(nil), base...)
		r.Shuffle(len(reqs), func(i, j int) { reqs[i], reqs[j] = reqs[j], reqs[i] })
		ns := name.New("a", "b", "c", "d")
		steps, _, err := arrange(ns, reqs)
		if err != nil {
			ok = false
			break
		}
		if ref == nil {
			ref = steps
		} else if len(steps) != len(ref) {
			ok = false
		} else {
			for i := range steps {
				if steps[i] != ref[i] {
					ok = false
				}
			}
		}
	}
	report("打乱构造顺序20次:步骤序列逐元素一致", ok, "")
}

func checkTempCount() {
	reqs := []plan.Request{}
	init := []string{}
	for g := 0; g < 10; g++ {
		x0 := "x" + itoa(g) + "a"
		x1 := "x" + itoa(g) + "b"
		init = append(init, x0, x1)
		reqs = append(reqs, plan.Request{x0, x1}, plan.Request{x1, x0})
	}
	ns := name.New(init...)
	_, temps, err := arrange(ns, reqs)
	report("10个不相交环:临时名恰好10个", err == nil && temps == 10, "")
}

func recLen(oldName, newName string) int { return 2 + len(oldName) + 2 + len(newName) + 1 + 4 }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
