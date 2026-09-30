package staleness

import "fmt"

// Builder 用于声明资产与依赖边并构建 Engine。
type Builder struct {
	assets map[string]*asset
	order  []string // 资产声明次序，保证行为确定
}

// NewBuilder 返回一个空的 Builder。
func NewBuilder() *Builder {
	return &Builder{assets: make(map[string]*asset)}
}

// AddAsset 声明一个资产及其分区范围 [first,last]。
func (b *Builder) AddAsset(name string, first, last int) error {
	if first > last {
		return newError(KindInvalidRange,
			fmt.Sprintf("资产 %q 的声明范围非法：first=%d > last=%d", name, first, last))
	}
	if _, ok := b.assets[name]; ok {
		return newError(KindDuplicateAsset, fmt.Sprintf("资产 %q 重复声明", name))
	}
	b.assets[name] = &asset{
		name:       name,
		first:      first,
		last:       last,
		partitions: make(map[int]*partition),
	}
	b.order = append(b.order, name)
	return nil
}

// AddEdge 声明一条上游到下游的依赖边，下游分区 d 读取上游 [d+lo, d+hi]。
func (b *Builder) AddEdge(upstream, downstream string, lo, hi int) error {
	up, ok := b.assets[upstream]
	if !ok {
		return newError(KindUnknownAsset, fmt.Sprintf("依赖边上上游未知资产 %q", upstream))
	}
	down, ok := b.assets[downstream]
	if !ok {
		return newError(KindUnknownAsset, fmt.Sprintf("依赖边下游未知资产 %q", downstream))
	}
	for _, e := range down.edges {
		if e.upstream.name == upstream {
			return newError(KindDuplicateEdge,
				fmt.Sprintf("依赖边 %q -> %q 重复声明", upstream, downstream))
		}
	}
	if lo > hi {
		return newError(KindInvalidOffset,
			fmt.Sprintf("依赖边 %q -> %q 偏移非法：lo=%d > hi=%d", upstream, downstream, lo, hi))
	}
	down.edges = append(down.edges, edge{upstream: up, lo: lo, hi: hi})
	return nil
}

// Build 校验依赖无环、计算层深并返回可用的 Engine。
func (b *Builder) Build() (*Engine, error) {
	// Kahn 拓扑排序：入度为上游边数，源（无上游）层深为 0。
	indegree := make(map[string]int, len(b.assets))
	for _, name := range b.order {
		indegree[name] = len(b.assets[name].edges)
	}
	queue := make([]string, 0, len(b.order))
	for _, name := range b.order {
		if indegree[name] == 0 {
			queue = append(queue, name)
		}
	}
	processed := 0
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		processed++
		a := b.assets[name]
		depth := 0
		for _, e := range a.edges {
			if e.upstream.depth+1 > depth {
				depth = e.upstream.depth + 1
			}
		}
		a.depth = depth
		for _, other := range b.order {
			for _, e := range b.assets[other].edges {
				if e.upstream.name == name {
					indegree[other]--
					if indegree[other] == 0 {
						queue = append(queue, other)
					}
				}
			}
		}
	}
	if processed < len(b.assets) {
		remaining := make([]string, 0)
		for _, name := range b.order {
			if indegree[name] > 0 {
				remaining = append(remaining, name)
			}
		}
		return nil, newError(KindCycle,
			fmt.Sprintf("依赖成环（含自依赖），涉及资产：%v", remaining))
	}
	return &Engine{
		assets: b.assets,
		runs:   make(map[int]*run),
	}, nil
}
