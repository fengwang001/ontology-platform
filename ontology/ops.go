package ontology

import (
	"fmt"
	"strings"
)

// stale 判断已物化分区在当前快照下是否过期；未物化返回 false（缺失不算过期）。
// 调用方需持锁。why 累积判定依据，供日志使用。
func (p *Planner) stale(k partKey, memo map[partKey]bool, why map[partKey][]string) bool {
	if v, ok := memo[k]; ok {
		return v
	}
	s := p.parts[k]
	if s == nil || !s.materialized {
		memo[k] = false
		return false
	}

	var reasons []string
	result := false
	for in, consumedVer := range s.consumed {
		cur := p.parts[in]
		// 缺失分区不是“过期”：已物化分区的消费记录只可能指向当时已物化的输入，
		// 该输入此后版本只能单调增加，因此记录中的版本与缺失状态不可能共存；
		// 未物化输入不参与过期递归。
		if cur == nil || !cur.materialized {
			continue
		}
		if consumedVer != cur.version {
			result = true
			reasons = append(reasons, fmt.Sprintf("input %s#%d version %d != consumed %d",
				in.asset, in.part, cur.version, consumedVer))
			continue
		}
		if p.stale(in, memo, why) {
			result = true
			reasons = append(reasons, fmt.Sprintf("input %s#%d is itself stale", in.asset, in.part))
		}
	}
	memo[k] = result
	if result {
		why[k] = reasons
	}
	return result
}

func (p *Planner) freshnessSnapshot() (map[partKey]bool, map[partKey][]string) {
	memo := map[partKey]bool{}
	why := map[partKey][]string{}
	for k, s := range p.parts {
		if s.materialized {
			p.stale(k, memo, why)
		}
	}
	return memo, why
}

// ExternalWrite 对源资产分区做一次外部写入。
func (p *Planner) ExternalWrite(assetName string, part int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	a, err := p.lookup(assetName, part)
	if err != nil {
		p.logger.Printf("external-write REJECT asset=%s part=%d reason=%s", assetName, part, err.(*Error).Reason)
		return err
	}
	key := partKey{assetName, part}
	s := p.parts[key]
	if len(p.inputs[assetName]) != 0 {
		err := newError(ReasonExternalOnDerived,
			fmt.Sprintf("asset %q is not a source", assetName))
		p.logger.Printf("external-write REJECT asset=%s part=%d reason=%s", assetName, part, err.Reason)
		return err
	}

	if s == nil {
		s = p.state(key)
	}
	first := !s.materialized
	s.version++
	s.materialized = true
	p.logger.Printf("external-write OK asset=%s part=%d first=%d newVersion=%d",
		a.name, part, btoi(first), s.version)
	return nil
}

// Start 尝试开始物化：所有输入必须已物化且不过期，且该分区无运行中的物化。
func (p *Planner) Start(assetName string, part int) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	a, err := p.lookup(assetName, part)
	if err != nil {
		p.logger.Printf("start REJECT asset=%s part=%d reason=%s", assetName, part, err.(*Error).Reason)
		return 0, err
	}
	key := partKey{assetName, part}
	s := p.parts[key]
	if s != nil && s.running {
		err := newError(ReasonAlreadyRunning,
			fmt.Sprintf("partition %s#%d already has a running materialization", assetName, part))
		p.logger.Printf("start REJECT asset=%s part=%d reason=%s", assetName, part, err.Reason)
		return 0, err
	}

	refs := p.inputRefs(assetName, part)
	memo, why := p.freshnessSnapshot()
	var notReady []NotReadyInput
	for _, k := range refs {
		ks := p.parts[k]
		switch {
		case ks == nil || !ks.materialized:
			notReady = append(notReady, NotReadyInput{Asset: k.asset, Partition: k.part, Missing: true})
		case memo[k]:
			notReady = append(notReady, NotReadyInput{Asset: k.asset, Partition: k.part, Missing: false})
		}
	}
	if len(notReady) != 0 {
		err := newError(ReasonInputNotReady,
			fmt.Sprintf("partition %s#%d has %d input(s) missing or stale", assetName, part, len(notReady)))
		err.Details["inputs"] = notReady
		p.logger.Printf("start REJECT asset=%s part=%d reason=%s inputs=%s",
			assetName, part, err.Reason, formatNotReady(notReady, memo, why))
		return 0, err
	}

	p.nextRun++
	id := p.nextRun
	snapshot := map[partKey]int64{}
	for _, k := range refs {
		snapshot[k] = p.parts[k].version
	}
	if s == nil {
		s = p.state(key)
	}
	p.runs[id] = &run{id: id, target: key, inputs: snapshot}
	s.running = true

	p.logger.Printf("start OK asset=%s part=%d run=%d depth=%d inputs=%s",
		a.name, part, id, a.depth, formatSnapshot(snapshot))
	return id, nil
}

// Complete 结束物化运行；success=false 不改变任何状态。
func (p *Planner) Complete(runID int64, success bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	r, ok := p.runs[runID]
	if !ok {
		err := newError(ReasonRunNotFound, fmt.Sprintf("run %d does not exist", runID))
		p.logger.Printf("complete REJECT run=%d reason=%s", runID, err.Reason)
		return err
	}
	if r.finished {
		err := newError(ReasonRunFinished, fmt.Sprintf("run %d already finished", runID))
		p.logger.Printf("complete REJECT run=%d reason=%s", runID, err.Reason)
		return err
	}

	target := p.state(r.target)
	r.finished = true
	r.success = success
	target.running = false
	if !success {
		p.logger.Printf("complete OK run=%d asset=%s part=%d success=false (state unchanged)",
			runID, r.target.asset, r.target.part)
		return nil
	}

	target.version++
	target.materialized = true
	target.consumed = map[partKey]int64{}
	for k, v := range r.inputs {
		target.consumed[k] = v
		p.indexAdd(k, r.target)
	}

	// 以本次完成后的快照判定是否“一落地即过期”。
	memo, why := p.freshnessSnapshot()
	staleNow := memo[r.target]
	p.logger.Printf("complete OK run=%d asset=%s part=%d success=true newVersion=%d staleAtBirth=%v why=%s",
		runID, r.target.asset, r.target.part, target.version, staleNow, strings.Join(why[r.target], "; "))
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatSnapshot(s map[partKey]int64) string {
	ks := make([]partKey, 0, len(s))
	for k := range s {
		ks = append(ks, k)
	}
	sortKeys(ks)
	var b strings.Builder
	for i, k := range ks {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s#%d@%d", k.asset, k.part, s[k])
	}
	return b.String()
}

func formatNotReady(xs []NotReadyInput, memo map[partKey]bool, why map[partKey][]string) string {
	var b strings.Builder
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		state := "missing"
		if !x.Missing {
			state = "stale"
		}
		fmt.Fprintf(&b, "%s#%d(%s)", x.Asset, x.Partition, state)
	}
	return b.String()
}
