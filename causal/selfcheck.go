package causal

import (
	"fmt"
	"slices"

	"ontology/vc"
)

// delivered 记录一次已投递事件，用于从零向量朴素重算。
type delivered struct {
	from int
	m    vc.Message
}

// SelfCheck 对一组内置操作序列独立核验四条不变量，全部通过返回 nil。
// 它不复用被测路径之外的任何缓存：另起零向量逐消息重算。
func (s *System) SelfCheck() error {
	n := 3
	t := New(n)
	log := make([][]delivered, n) // 每个节点真实发生的投递顺序
	step := func(ok bool, err error, wantOK bool, name string) error {
		if err != nil || ok != wantOK {
			return fmt.Errorf("selfcheck %s: ok=%v err=%v want %v", name, ok, err, wantOK)
		}
		return nil
	}
	doDeliver := func(p int, m vc.Message, wantOK bool, name string) error {
		before, _ := t.VC(p)
		ok, err := t.Deliver(p, m)
		if e := step(ok, err, wantOK, name); e != nil {
			return e
		}
		if !ok { // 阻塞必须不留痕
			after, _ := t.VC(p)
			if !equalClock(before, after) {
				return fmt.Errorf("selfcheck %s: blocked deliver changed state", name)
			}
			return nil
		}
		// 独立校验不变量 2、3，再记入朴素重算日志
		cur := naive(log[p], n)
		if !vc.Deliverable(cur, m.TS, m.From) {
			return fmt.Errorf("selfcheck %s: delivery violated causal/gap rule", name)
		}
		log[p] = append(log[p], delivered{m.From, m})
		return nil
	}

	doBroadcast := func(from int, name string) (vc.Message, error) {
		m, err := t.Broadcast(from)
		if err != nil {
			return m, fmt.Errorf("selfcheck %s: %w", name, err)
		}
		log[from] = append(log[from], delivered{from, m}) // 自发自投：计入该节点重算
		return m, nil
	}

	m1, err := doBroadcast(0, "S1") // S1
	if err != nil {
		return err
	}
	if e := doDeliver(1, m1, true, "S2"); e != nil {
		return e
	}
	m2, err := doBroadcast(1, "S3") // S3
	if err != nil {
		return err
	}
	if e := doDeliver(2, m1, true, "S4"); e != nil {
		return e
	}
	if e := doDeliver(2, m2, true, "S5"); e != nil {
		return e
	}
	m3, err := doBroadcast(1, "S6") // S6
	if err != nil {
		return err
	}
	if e := doDeliver(0, m3, false, "S7-block"); e != nil {
		return e
	}

	// 不变量 1：每节点当前 VC 必须等于按投递顺序朴素重算的结果。
	for p := 0; p < n; p++ {
		got, _ := t.VC(p)
		if want := naive(log[p], n); !equalClock(got, want) {
			return fmt.Errorf("selfcheck invariant1 node %d: got %v want %v", p, got, want)
		}
	}
	// 不变量 4：三类故障拒绝后全节点状态快照不变，且系统仍可正常使用。
	snap := map[int]vc.Clock{}
	for p := 0; p < n; p++ {
		snap[p], _ = t.VC(p)
	}
	bad := []struct {
		node int
		m    vc.Message
	}{
		{9, m1},                           // 下标越界
		{0, vc.Message{From: 1, TS: nil}}, // 未知消息
		{1, m2},                           // 自投递
	}
	for i, b := range bad {
		if _, err := t.Deliver(b.node, b.m); err == nil {
			return fmt.Errorf("selfcheck fault %d: want error", i)
		}
	}
	for p := 0; p < n; p++ {
		got, _ := t.VC(p)
		if !equalClock(got, snap[p]) {
			return fmt.Errorf("selfcheck invariant4: node %d changed %v->%v", p, snap[p], got)
		}
	}
	if ok, err := t.Deliver(0, m2); !ok || err != nil { // 拒绝后仍可正常投递
		return fmt.Errorf("selfcheck recovery after faults: ok=%v err=%v", ok, err)
	}
	// O(1) 定位：节点 1 缓冲 m 条后，阻塞投递最后一条，检查数恒为 1，不随 m 增长。
	for _, m := range []int{100, 1000, 10000} {
		u := New(2)
		var last vc.Message
		for i := 0; i < m; i++ {
			last, err = u.Broadcast(0)
			if err != nil {
				return err
			}
		}
		if ok, err := u.Deliver(1, last); ok || err != nil || u.lastCheck != 1 {
			return fmt.Errorf("selfcheck lookup m=%d: ok=%v err=%v checked=%d", m, ok, err, u.lastCheck)
		}
	}
	return nil
}

// naive 从零向量起，按投递顺序把每条消息的发送者分量推进，返回重算时钟。
func naive(log []delivered, n int) vc.Clock {
	c := make(vc.Clock, n)
	for _, d := range log {
		c[d.from]++
	}
	return c
}

// equalClock 逐字段比较两个时钟。
func equalClock(a, b vc.Clock) bool { return slices.Equal(a, b) }
