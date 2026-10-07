package ontology

// ledger 维护「每个具体实例在每个链接类型的一侧」上当前已占用的基数名额。
//
// 键设计刻意只包含 (链接类型, 侧, 实例)，计数与该对象类型下链接总数无关：
// 校验一次创建只读取两个计数器，创建/删除只增减两个计数器，全部为 map 的
// 常数时间操作。批次内的临时占用与已提交占用共用同一份计数器（详见 service
// 层的 apply/rollback），因此同批次累积与跨操作可见性语义天然一致。
//
// ledger 自身不加锁；所有方法都要求在 service 的单一互斥临界区内调用，
// 并发串行化由 service 统一保证。
type ledger struct {
	source map[sideKey]int
	target map[sideKey]int
	// probe 非 nil 时，记录 check 过程中发生的计数器访问次数。
	// 一次 check 固定访问恰好 2 个计数器，与数据规模无关——这是
	// 「校验开销不随链接总数增长」的可机器验证依据。
	probe *probeCounter
}

type sideKey struct {
	linkType string
	instance string
}

// probeCounter 是校验成本探针：只统计 check 的计数器访问次数。
type probeCounter struct{ accesses int }

func newLedger() *ledger {
	return &ledger{
		source: map[sideKey]int{},
		target: map[sideKey]int{},
	}
}

func newLedgerWithProbe(p *probeCounter) *ledger {
	return &ledger{
		source: map[sideKey]int{},
		target: map[sideKey]int{},
		probe:  p,
	}
}

// check 判断在接受 (linkType, p) 后两端是否仍在声明上限内。
// 返回 sourceExceeded / targetExceeded，两端独立判定。
// 访问的计数器数量恒为 2（每端一个），可由 probe 验证。
func (l *ledger) check(decl LinkTypeDecl, p Pair) (sourceExceeded, targetExceeded bool) {
	sourceUsed := l.source[sideKey{decl.Name, p.Source}]
	targetUsed := l.target[sideKey{decl.Name, p.Target}]
	if l.probe != nil {
		l.probe.accesses += 2
	}
	if cap, limited := decl.SourceCap.Cap(); limited && sourceUsed+1 > cap {
		sourceExceeded = true
	}
	if cap, limited := decl.TargetCap.Cap(); limited && targetUsed+1 > cap {
		targetExceeded = true
	}
	return sourceExceeded, targetExceeded
}

// apply 记录一条链接对两端计数器的占用。
func (l *ledger) apply(linkType string, p Pair) {
	l.source[sideKey{linkType, p.Source}]++
	l.target[sideKey{linkType, p.Target}]++
}

// release 释放一条链接对两端计数器的占用（删除与批次回滚共用）。
func (l *ledger) release(linkType string, p Pair) {
	k := sideKey{linkType, p.Source}
	v := l.source[k]
	if v <= 1 {
		delete(l.source, k)
	} else {
		l.source[k] = v - 1
	}
	k = sideKey{linkType, p.Target}
	v = l.target[k]
	if v <= 1 {
		delete(l.target, k)
	} else {
		l.target[k] = v - 1
	}
}

// usedSource / usedTarget 供测试与朴素模型对账使用。
func (l *ledger) usedSource(linkType, instance string) int {
	return l.source[sideKey{linkType, instance}]
}
func (l *ledger) usedTarget(linkType, instance string) int {
	return l.target[sideKey{linkType, instance}]
}
