package doc

import "fmt"

var eightWant = []string{"a", "ab", "abx", "abcx", "abcxy", "bcxy", "zbcxy", "zcxy"}

// eightSteps 在 d 上按题面执行八步，返回每步后的可见文本。
func eightSteps(d *Doc) ([]string, error) {
	I := func(prev, x ID, ch rune) error { return d.Insert(prev, x, ch) }
	steps := []func() error{
		func() error { return I(Empty, id(1, "A"), 'a') },
		func() error { return I(id(1, "A"), id(2, "A"), 'b') },
		func() error { return I(id(1, "A"), id(1, "B"), 'x') },
		func() error { return I(id(2, "A"), id(3, "A"), 'c') },
		func() error { return I(id(1, "B"), id(2, "B"), 'y') },
		func() error { return d.Delete(id(1, "A")) },
		func() error { return I(id(1, "A"), id(3, "B"), 'z') },
		func() error { return d.Delete(id(2, "A")) },
	}
	out := make([]string, 0, 8)
	for _, s := range steps {
		if err := s(); err != nil {
			return nil, err
		}
		out = append(out, d.Text())
	}
	return out, nil
}

// permConverge 枚举同一 prev 下并发子元素的所有到达顺序，要求结果恒为 a321。
func permConverge() error {
	children := []ID{id(3, "R3"), id(1, "R1"), id(2, "R2")}
	seen := ""
	var perm func(int) error
	perm = func(k int) error {
		if k == len(children) {
			e := New()
			_ = e.Insert(Empty, id(1, "A"), 'a')
			for _, c := range children {
				_ = e.Insert(id(1, "A"), c, rune('0'+c.Lamport))
			}
			txt := e.Text()
			if seen == "" {
				seen = txt
			} else if txt != seen {
				return fmt.Errorf("convergence: %q vs %q", txt, seen)
			}
			return nil
		}
		for i := k; i < len(children); i++ {
			children[k], children[i] = children[i], children[k]
			if err := perm(k + 1); err != nil {
				return err
			}
			children[k], children[i] = children[i], children[k]
		}
		return nil
	}
	if err := perm(0); err != nil {
		return err
	}
	if seen != "a321" {
		return fmt.Errorf("converged text %q want a321", seen)
	}
	return nil
}

// SelfCheck 用内置操作序列核验四条不变量与复杂度，全部通过返回 nil。
// 计算都在局部文档与日志快照上进行，可被多 goroutine 并发调用。
func (d *Doc) SelfCheck() error {
	log, cur := d.snapshot()
	if s := naiveText(log); s != cur { // 不变量 1
		return fmt.Errorf("naive reference mismatch: %q vs %q", s, cur)
	}

	got, err := eightSteps(New()) // 不变量 1/2/3：八步全程
	if err != nil {
		return err
	}
	if fmt.Sprint(got) != fmt.Sprint(eightWant) {
		return fmt.Errorf("eight steps got %v want %v", got, eightWant)
	}
	if err := permConverge(); err != nil { // 不变量 2
		return err
	}

	// 不变量 3：墓碑后仍可定位插入，子孙保留。
	t := New()
	_ = t.Insert(Empty, id(1, "A"), 'a')
	_ = t.Insert(id(1, "A"), id(2, "A"), 'b')
	if err := t.Delete(id(1, "A")); err != nil {
		return err
	}
	if err := t.Insert(id(1, "A"), id(5, "B"), 'q'); err != nil {
		return fmt.Errorf("insert after tombstone: %w", err)
	}
	if t.Text() != "qb" {
		return fmt.Errorf("tombstone text %q want qb", t.Text())
	}

	// 不变量 4：三类哨兵互不相同，被拒后文本不变且仍可正常使用。
	bad := New()
	if err := bad.Insert(Empty, id(1, "A"), 'a'); err != nil {
		return err
	}
	errs := []error{
		bad.Insert(Empty, id(1, "A"), 'x'),
		bad.Insert(id(9, "X"), id(2, "A"), 'x'),
		bad.Insert(Empty, ID{Lamport: 1}, 'x'),
	}
	want := []error{ErrDuplicateID, ErrPrevNotFound, ErrInvalidID}
	for i := range errs {
		if errs[i] != want[i] {
			return fmt.Errorf("error %d got %v want %v", i, errs[i], want[i])
		}
	}
	if bad.Text() != "a" {
		return fmt.Errorf("rejected op traced: %q", bad.Text())
	}
	if err := bad.Insert(id(1, "A"), id(2, "A"), 'b'); err != nil {
		return fmt.Errorf("doc unusable after rejection: %w", err)
	}

	// 复杂度：多档 m，哈希定位探针不随 m 增长（常数 1）。
	for _, m := range []int{100, 1000, 10000} {
		e := New()
		for i := 1; i <= m; i++ {
			prev := Empty
			if i > 1 {
				prev = id(uint64(i-1), "R")
			}
			if err := e.Insert(prev, id(uint64(i), "R"), 'x'); err != nil {
				return err
			}
		}
		if err := e.Insert(id(uint64(m/2), "R"), id(uint64(m+1), "Q"), 'z'); err != nil {
			return err
		}
		if e.lastProbe > 1 {
			return fmt.Errorf("probe %d grows with m=%d", e.lastProbe, m)
		}
	}
	return nil
}
