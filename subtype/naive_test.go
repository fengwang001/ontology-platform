package subtype_test

import (
	"fmt"

	"ontology/subtype"
)

// 本文件实现一个独立于主判定器的朴素模型，作为随机对照测试的参照：
// 把递归展开到足够深度后比较。展开深度（燃料）取两侧可达命名类型
// 并集的两两组合总数 + 1：任何更长的 (引用, 引用) 展开路径必然重复
// 某个命名对，而共归纳语义下重复的命名对视为成立，故燃料耗尽时
// 返回 true 与主判定器一致。

// naiveReachable 独立实现的静态可达名字收集。
func naiveReachable(defs map[string]subtype.Type, t subtype.Type, out map[string]bool) {
	switch t := t.(type) {
	case subtype.Object:
		for _, p := range t.Props {
			naiveReachable(defs, p.Type, out)
		}
	case subtype.Func:
		for _, p := range t.Params {
			naiveReachable(defs, p, out)
		}
		naiveReachable(defs, t.Return, out)
	case subtype.Union:
		for _, m := range t.Members {
			naiveReachable(defs, m, out)
		}
	case subtype.Ref:
		if out[t.Name] {
			return
		}
		out[t.Name] = true
		if def, ok := defs[t.Name]; ok {
			naiveReachable(defs, def, out)
		}
	}
}

// naiveFuel 计算朴素模型所需的展开深度。
func naiveFuel(defs map[string]subtype.Type, l, r subtype.Type) int {
	names := make(map[string]bool)
	naiveReachable(defs, l, names)
	naiveReachable(defs, r, names)
	return len(names)*len(names) + 1
}

// naiveChecker 朴素判定器的状态：定义快照、剩余燃料与步数保险丝。
type naiveChecker struct {
	defs  map[string]subtype.Type
	steps int // 保险丝：防止实现错误导致死循环
}

// naiveSubtype 用朴素展开模型判定 l 是否为 r 的子类型。
func naiveSubtype(defs map[string]subtype.Type, l, r subtype.Type) (bool, error) {
	c := &naiveChecker{defs: defs, steps: 1_000_000}
	return c.check(l, r, naiveFuel(defs, l, r))
}

func (c *naiveChecker) check(l, r subtype.Type, fuel int) (bool, error) {
	c.steps--
	if c.steps <= 0 {
		return false, fmt.Errorf("朴素模型步数超限（疑似死循环）: %v <: %v", l, r)
	}
	// 顶类型与底类型。
	if _, ok := r.(subtype.Top); ok {
		return true, nil
	}
	if _, ok := l.(subtype.Bottom); ok {
		return true, nil
	}
	// 联合。
	if lu, ok := l.(subtype.Union); ok {
		for _, m := range lu.Members {
			ok, err := c.check(m, r, fuel)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	if ru, ok := r.(subtype.Union); ok {
		for _, m := range ru.Members {
			ok, err := c.check(l, m, fuel)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	// 引用。
	lr, lIsRef := l.(subtype.Ref)
	rr, rIsRef := r.(subtype.Ref)
	switch {
	case lIsRef && rIsRef:
		if lr.Name == rr.Name {
			return true, nil
		}
		if fuel <= 0 {
			// 展开深度已超过所有可能的命名对数量：
			// 路径上必然重复某个命名对，按共归纳视为成立。
			return true, nil
		}
		return c.check(c.defs[lr.Name], c.defs[rr.Name], fuel-1)
	case lIsRef:
		return c.check(c.defs[lr.Name], r, fuel)
	case rIsRef:
		return c.check(l, c.defs[rr.Name], fuel)
	}
	// 结构规则。
	switch lt := l.(type) {
	case subtype.Int:
		switch r.(type) {
		case subtype.Int, subtype.Float:
			return true, nil
		}
		return false, nil
	case subtype.Float:
		_, ok := r.(subtype.Float)
		return ok, nil
	case subtype.Str:
		_, ok := r.(subtype.Str)
		return ok, nil
	case subtype.Bool:
		_, ok := r.(subtype.Bool)
		return ok, nil
	case subtype.Top:
		return false, nil
	case subtype.Object:
		rt, ok := r.(subtype.Object)
		if !ok {
			return false, nil
		}
		return c.checkObject(lt, rt, fuel)
	case subtype.Func:
		rt, ok := r.(subtype.Func)
		if !ok {
			return false, nil
		}
		return c.checkFunc(lt, rt, fuel)
	}
	return false, fmt.Errorf("未知类型表达式: %v", l)
}

func (c *naiveChecker) checkObject(s, t subtype.Object, fuel int) (bool, error) {
	sProps := make(map[string]subtype.Prop, len(s.Props))
	for _, p := range s.Props {
		sProps[p.Name] = p
	}
	for _, tp := range t.Props {
		sp, ok := sProps[tp.Name]
		if !ok {
			if !tp.Optional {
				return false, nil
			}
			continue
		}
		if !tp.Optional && sp.Optional {
			return false, nil
		}
		if tp.ReadOnly {
			ok, err := c.check(sp.Type, tp.Type, fuel)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
			continue
		}
		if sp.ReadOnly {
			return false, nil
		}
		forward, err := c.check(sp.Type, tp.Type, fuel)
		if err != nil {
			return false, err
		}
		backward, err := c.check(tp.Type, sp.Type, fuel)
		if err != nil {
			return false, err
		}
		if !forward || !backward {
			return false, nil
		}
	}
	return true, nil
}

func (c *naiveChecker) checkFunc(s, t subtype.Func, fuel int) (bool, error) {
	if len(s.Params) > len(t.Params) {
		return false, nil
	}
	for i, sp := range s.Params {
		ok, err := c.check(t.Params[i], sp, fuel)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return c.check(s.Return, t.Return, fuel)
}
