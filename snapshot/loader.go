package snapshot

import (
	"fmt"
	"sort"
)

// Loader 对一份已落盘的导出做只读校验；并发安全、结论确定可重复。
// Loader 自身不持有任何可变状态：每次请求都从磁盘重新读取并独立判定，
// 因此多个 goroutine 并发校验同一份导出必然得到完全相同的结论。
type Loader struct {
	dir string
}

// NewLoader 创建只读加载器。
func NewLoader(dir string) *Loader { return &Loader{dir: dir} }

// evaluate 是 Load/Aggregate 共用的纯只读判定流程：
//  1. 读清单确定导出覆盖范围；
//  2. 全部块独立完整性校验 + 条数核对（彼此无关）；
//  3. 范围外请求以最高优先级记出；
//  4. 对请求类型的可信块做跨块引用保守判定。
//
// 任何阶段都不写回磁盘，也不缓存可变结论。
func (l *Loader) evaluate(types []string, logger DecisionLogger) (*manifestFile, map[ChunkRef]*ChunkReport, []Issue) {
	if logger == nil {
		logger = nopLogger{}
	}
	requested := uniqueSorted(types)

	mf, err := readManifest(l.dir)
	if err != nil {
		logger.Log(Decision{
			Stage:  "manifest",
			Input:  l.dir,
			Output: "unreadable",
			Basis:  "清单无法读取，导出范围未知",
		})
		return nil, nil, []Issue{{
			Kind:   KindIntegrityFailure,
			Chunk:  ChunkRef{},
			Reason: "manifest unreadable: " + err.Error(),
		}}
	}
	covered := map[string]bool{}
	for _, t := range mf.Types {
		covered[t] = true
	}
	requestedSet := map[string]bool{}
	for _, t := range requested {
		requestedSet[t] = true
	}

	issues := []Issue{}
	for _, t := range requested {
		if !covered[t] {
			issues = append(issues, Issue{
				Kind:   KindOutOfRange,
				Chunk:  ChunkRef{Type: t, Chunk: -1},
				Reason: fmt.Sprintf("type %q not covered by this export", t),
			})
			logger.Log(Decision{
				Stage:  "scope",
				Input:  "requested type=" + t,
				Output: string(KindOutOfRange),
				Basis:  "请求类型不在本次导出覆盖范围内（最高拒绝优先级）",
			})
		}
	}

	reports := verifyAll(l.dir, mf)
	for _, ref := range sortedChunkRefs(reports) {
		rep := reports[ref]
		// 只有被请求且在覆盖范围内的类型，其块级问题才作为本次请求命中报告；
		// 未参与类型的块仍会被独立校验（Reports 可见），但不命中本次请求。
		if !covered[rep.Chunk.Type] || !requestedSet[rep.Chunk.Type] {
			logger.Log(Decision{
				Stage:  "chunk_verify",
				Chunk:  &rep.Chunk,
				Input:  fmt.Sprintf("declared=%d actual=%d (not requested)", rep.Declared, rep.Actual),
				Output: string(rep.Status),
				Basis:  "块独立校验完成，但该类型不在本次请求内，不作为命中项",
			})
			continue
		}
		switch rep.Status {
		case StatusIntegrityBad:
			issues = append(issues, Issue{
				Kind: KindIntegrityFailure, Chunk: rep.Chunk,
				Reason: rep.Reason,
			})
		case StatusCountMismatch:
			issues = append(issues, Issue{
				Kind: KindCountMismatch, Chunk: rep.Chunk,
				Reason: rep.Reason,
			})
		}
		logger.Log(Decision{
			Stage:  "chunk_verify",
			Chunk:  &rep.Chunk,
			Input:  fmt.Sprintf("declared=%d actual=%d", rep.Declared, rep.Actual),
			Output: string(rep.Status),
			Basis:  chunkBasis(rep.Status),
		})
	}

	// 引用检查只针对“在覆盖范围内且被请求”的类型；
	// 范围外请求类型没有可校验的源块。
	inScopeRequested := make([]string, 0, len(requested))
	for _, t := range requested {
		if covered[t] {
			inScopeRequested = append(inScopeRequested, t)
		}
	}
	issues = append(issues, checkReferences(inScopeRequested, reports, logger)...)

	return mf, reports, orderIssues(issues)
}

func chunkBasis(status ChunkStatus) string {
	switch status {
	case StatusTrusted:
		return "sha256(records) 匹配且声明条数 == 实际条数"
	case StatusCountMismatch:
		return "校验通过但条数不符：无法区分截断/多算，整块记录不可信，即使部分记录本身校验通过"
	default:
		return "块内容校验信息不匹配：完整性失败，结论只针对本块"
	}
}

func uniqueSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Load 校验并取出所请求类型的数据。
// 即使聚合视图不可生成，已通过校验的单个块数据仍可独立取出：
// LoadResult.Reports 中 trusted 块的 Records 始终可用。
func (l *Loader) Load(types []string, logger DecisionLogger) (*LoadResult, error) {
	if logger == nil {
		logger = nopLogger{}
	}
	_, reports, issues := l.evaluate(types, logger)
	return &LoadResult{Reports: reports, Issues: issues}, nil
}

// Aggregate 尝试为所请求类型生成聚合视图。
// 仅当全部参与类型在覆盖范围内、其全部块可信、且这些块的所有
// 跨块引用均可解析（不存在悬空，也不存在无法校验）时才生成视图；
// 任一条件不满足，Aggregatable=false 并给出命中块与原因。
func (l *Loader) Aggregate(types []string, logger DecisionLogger) (*AggregateResult, error) {
	if logger == nil {
		logger = nopLogger{}
	}
	mf, reports, issues := l.evaluate(types, logger)
	res := &AggregateResult{Issues: issues, Objects: map[string][]Record{}}
	if mf == nil {
		return res, nil
	}
	requested := uniqueSorted(types)

	blocking := map[IssueKind]bool{
		KindOutOfRange:       true,
		KindIntegrityFailure: true,
		KindCountMismatch:    true,
		KindDanglingRef:      true,
		KindRefUnverifiable:  true,
	}
	for _, is := range issues {
		if blocking[is.Kind] {
			logger.Log(Decision{
				Stage:  "aggregate",
				Chunk:  &is.Chunk,
				Input:  "aggregate requested types",
				Output: "not_generatable",
				Basis:  "命中问题类别 " + string(is.Kind) + "，聚合视图整体不可生成（单块可取性不受影响）",
			})
			return res, nil
		}
	}

	for _, typ := range requested {
		var recs []Record
		// 块按 chunk index 顺序合并，保证聚合结果确定可重复。
		chunkFiles := mf.Chunks[typ]
		for idx := range chunkFiles {
			rep := reports[ChunkRef{Type: typ, Chunk: idx}]
			if rep == nil || rep.Status != StatusTrusted {
				chunk := ChunkRef{Type: typ, Chunk: idx}
				logger.Log(Decision{
					Stage: "aggregate", Chunk: &chunk,
					Input: typ, Output: "not_generatable",
					Basis: "参与聚合的块不全可信",
				})
				return &AggregateResult{Issues: issues, Objects: map[string][]Record{}}, nil
			}
			recs = append(recs, rep.Records...)
		}
		res.Objects[typ] = recs
	}
	res.Aggregatable = true
	logger.Log(Decision{
		Stage:  "aggregate",
		Input:  fmt.Sprintf("types=%v", requested),
		Output: "generated",
		Basis:  "全部参与块通过完整性校验、数量一致、相互引用全部可解析",
	})
	return res, nil
}
