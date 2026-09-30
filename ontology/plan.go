package ontology

import (
	"fmt"
	"strings"
)

// lookup 仅做资产与范围校验，不创建任何运行时状态。调用方需持锁。
func (p *Planner) lookup(assetName string, part int) (*asset, error) {
	a, ok := p.assets[assetName]
	if !ok {
		return nil, newError(ReasonUnknownAsset, "unknown asset "+assetName)
	}
	if part < a.first || part > a.last {
		return nil, newError(ReasonPartitionOutside, "partition outside declared range")
	}
	return a, nil
}

// Plan 返回目标本身及其全部传递输入中缺失或过期、需物化的分区；
// 新鲜分区不会进入结果。结果按 (层深, 资产名, 分区号) 升序。
func (p *Planner) Plan(targets []PartitionRef) ([]PartitionRef, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var queue []partKey
	queued := map[partKey]bool{}
	for _, t := range targets {
		if _, err := p.lookup(t.Asset, t.Partition); err != nil {
			p.logger.Printf("plan REJECT target=%s#%d reason=%s", t.Asset, t.Partition, err.(*Error).Reason)
			return nil, err
		}
		k := partKey{t.Asset, t.Partition}
		if !queued[k] {
			queued[k] = true
			queue = append(queue, k)
		}
	}

	memo, why := p.freshnessSnapshot()
	needed := map[partKey]bool{}
	var basis []string
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]

		s := p.parts[k]
		switch {
		case s == nil || !s.materialized:
			needed[k] = true
			basis = append(basis, fmt.Sprintf("%s#%d=missing", k.asset, k.part))
		case memo[k]:
			needed[k] = true
			basis = append(basis, fmt.Sprintf("%s#%d=stale(%s)", k.asset, k.part, strings.Join(why[k], ";")))
		}

		// 无论自身是否新鲜，都要遍历全部传递输入。
		for _, in := range p.inputRefs(k.asset, k.part) {
			if !queued[in] {
				queued[in] = true
				queue = append(queue, in)
			}
		}
	}

	out := keysOf(needed)
	sortPartKeys(p.assets, out)
	p.logger.Printf("plan OK targets=[%s] result=[%s] basis=[%s]",
		formatRefs(targets), formatKeys(out), strings.Join(basis, "; "))
	return refsToAPI(out), nil
}

// Impact 返回某分区若被重写，将传递变为过期的已物化下游分区。
// 结果按 (层深, 资产名, 分区号) 升序，不含被重写分区自身。
func (p *Planner) Impact(assetName string, part int) ([]PartitionRef, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if _, err := p.lookup(assetName, part); err != nil {
		p.logger.Printf("impact REJECT asset=%s part=%d reason=%s", assetName, part, err.(*Error).Reason)
		return nil, err
	}

	root := partKey{assetName, part}
	affected := map[partKey]bool{}
	queue := []partKey{root}
	visited := map[partKey]bool{root: true}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for down := range p.reverseIndex[k] {
			// 反向索引只收录已物化下游；其消费记录中含 k，重写 k 必然产生版本不一致。
			if !affected[down] {
				affected[down] = true
			}
			if !visited[down] {
				visited[down] = true
				queue = append(queue, down)
			}
		}
	}

	out := keysOf(affected)
	sortPartKeys(p.assets, out)
	p.logger.Printf("impact OK root=%s#%d result=[%s]", assetName, part, formatKeys(out))
	return refsToAPI(out), nil
}

func keysOf(m map[partKey]bool) []partKey {
	out := make([]partKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func formatKeys(ks []partKey) string {
	var b strings.Builder
	for i, k := range ks {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s#%d", k.asset, k.part)
	}
	return b.String()
}

func formatRefs(rs []PartitionRef) string {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s#%d", r.Asset, r.Partition)
	}
	return b.String()
}
