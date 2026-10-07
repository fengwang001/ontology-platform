package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// readManifest 只读加载导出清单。
func readManifest(dir string) (*manifestFile, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var mf manifestFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, err
	}
	if mf.Chunks == nil {
		mf.Chunks = map[string][]string{}
	}
	return &mf, nil
}

// verifyChunk 对单个块做独立完整性校验。
// 结论只取决于该块自身文件内容，与任何其它块无关：
//  1. 文件可解析、算法可识别，否则 integrity_failure；
//  2. sha256(实际记录) == 块内 checksum，否则 integrity_failure；
//  3. 仅当校验通过后才核对声明条数，不符为 count_mismatch
//     —— 无法区分“被截断”还是“被多算”，整块记录一律不可信。
func verifyChunk(dir, file string) *ChunkReport {
	rep := &ChunkReport{Chunk: ChunkRef{}}
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		rep.Status = StatusIntegrityBad
		rep.Reason = "chunk unreadable: " + err.Error()
		return rep
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		rep.Status = StatusIntegrityBad
		rep.Reason = "chunk not parseable: " + err.Error()
		return rep
	}
	rep.Chunk = ChunkRef{Type: env.Header.Type, Chunk: env.Header.ChunkIndex}
	rep.Declared = env.Header.DeclaredCount
	if env.Header.ChecksumAlgo != checksumAlgoSHA256 {
		rep.Status = StatusIntegrityBad
		rep.Reason = "unsupported checksum algorithm: " + env.Header.ChecksumAlgo
		return rep
	}
	got, err := computeChecksum(env.Records)
	if err != nil {
		rep.Status = StatusIntegrityBad
		rep.Reason = "checksum error: " + err.Error()
		return rep
	}
	if got != env.Checksum {
		rep.Status = StatusIntegrityBad
		rep.Reason = fmt.Sprintf("checksum mismatch: header=%s actual=%s", env.Checksum, got)
		return rep
	}
	rep.Actual = len(env.Records)
	if rep.Declared != rep.Actual {
		rep.Status = StatusCountMismatch
		rep.Reason = fmt.Sprintf("declared count %d != actual count %d", rep.Declared, rep.Actual)
		rep.Records = nil
		return rep
	}
	rep.Status = StatusTrusted
	rep.Records = env.Records
	return rep
}

// verifyAll 对清单中全部块做独立校验，返回按 ChunkRef 排序的报告。
func verifyAll(dir string, mf *manifestFile) map[ChunkRef]*ChunkReport {
	reports := map[ChunkRef]*ChunkReport{}
	for _, typ := range mf.Types {
		for idx, file := range mf.Chunks[typ] {
			rep := verifyChunk(dir, file)
			if rep.Chunk.Type == "" {
				rep.Chunk = ChunkRef{Type: typ, Chunk: idx}
			}
			reports[rep.Chunk] = rep
		}
	}
	return reports
}

func sortedChunkRefs(reports map[ChunkRef]*ChunkReport) []ChunkRef {
	out := make([]ChunkRef, 0, len(reports))
	for ref := range reports {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Chunk < out[j].Chunk
	})
	return out
}

// buildIndexes 为每个类型的可信块建立 O(1) 查找集合。
func buildIndexes(reports map[ChunkRef]*ChunkReport) map[string]*countingIndex {
	byType := map[string][]string{}
	for _, rep := range reports {
		if rep.Status == StatusTrusted {
			for _, rec := range rep.Records {
				byType[rep.Chunk.Type] = append(byType[rep.Chunk.Type], rec.ID)
			}
		}
	}
	idx := map[string]*countingIndex{}
	for typ, ids := range byType {
		index := newCountingIndex(ids)
		for _, id := range ids {
			index.insert(id)
		}
		idx[typ] = index
	}
	return idx
}

func typeStatus(reports map[ChunkRef]*ChunkReport, typ string) (present bool, bad bool) {
	found := false
	for _, rep := range reports {
		if rep.Chunk.Type == typ {
			found = true
			if rep.Status != StatusTrusted {
				return true, true
			}
		}
	}
	return found, false
}

// checkReferences 检查请求类型下全部可信块的跨类型引用。
// 保守判定规则（不得互换）：
//   - 目标类型不在导出覆盖范围：无法判定，记 reference_unverifiable；
//   - 目标类型存在但任一块不可信：无法校验，记 reference_unverifiable；
//   - 目标类型全部可信：索引命中即有效，未命中才是 dangling_reference。
//
// 双向引用的两个方向由各自所属块的引用独立判定，互不提供证据。
func checkReferences(requested []string, reports map[ChunkRef]*ChunkReport, logger DecisionLogger) []Issue {
	indexes := buildIndexes(reports)
	issues := []Issue{}

	reqSet := map[string]bool{}
	for _, t := range requested {
		reqSet[t] = true
	}

	for _, ref := range sortedChunkRefs(reports) {
		rep := reports[ref]
		if rep.Status != StatusTrusted || !reqSet[rep.Chunk.Type] {
			continue
		}
		// 记录按确定性顺序遍历（JSON 数组保序）。
		for _, rec := range rep.Records {
			for _, link := range rec.Refs {
				src := ChunkRef{Type: rep.Chunk.Type, Chunk: rep.Chunk.Chunk}
				present, bad := typeStatus(reports, link.TargetType)
				switch {
				case !present:
					issues = append(issues, Issue{
						Kind: KindRefUnverifiable, Chunk: src, Ref: &link,
						Reason: "target type not covered by export",
					})
					logger.Log(Decision{
						Stage: "reference", Chunk: &src,
						Input:  fmt.Sprintf("source=%s/%s ref=%s/%s", rep.Chunk.Type, rec.ID, link.TargetType, link.TargetID),
						Output: string(KindRefUnverifiable),
						Basis:  "目标类型不在导出覆盖范围，悬空与否不可判定",
					})
				case bad:
					issues = append(issues, Issue{
						Kind: KindRefUnverifiable, Chunk: src, Ref: &link,
						Reason: "target type chunk is untrusted (integrity/count)",
					})
					logger.Log(Decision{
						Stage: "reference", Chunk: &src,
						Input:  fmt.Sprintf("source=%s/%s ref=%s/%s", rep.Chunk.Type, rec.ID, link.TargetType, link.TargetID),
						Output: string(KindRefUnverifiable),
						Basis:  "目标块数量不一致或校验失败，悬空与否不可判定，既不默认悬空也不默认有效",
					})
				default:
					found, probes := indexes[link.TargetType].Contains(link.TargetID)
					if !found {
						issues = append(issues, Issue{
							Kind: KindDanglingRef, Chunk: src, Ref: &link,
							Reason: "target object not found in trusted target chunks",
						})
						logger.Log(Decision{
							Stage: "reference", Chunk: &src,
							Input:  fmt.Sprintf("source=%s/%s ref=%s/%s probes=%d", rep.Chunk.Type, rec.ID, link.TargetType, link.TargetID, probes),
							Output: string(KindDanglingRef),
							Basis:  "目标类型全部块可信，O(1) 索引未命中 => 悬空",
						})
					} else {
						logger.Log(Decision{
							Stage: "reference", Chunk: &src,
							Input:  fmt.Sprintf("source=%s/%s ref=%s/%s probes=%d", rep.Chunk.Type, rec.ID, link.TargetType, link.TargetID, probes),
							Output: "resolved",
							Basis:  "目标类型全部块可信，O(1) 索引命中",
						})
					}
				}
			}
		}
	}
	return issues
}

// orderIssues 按拒绝优先级排序：
// out_of_range > integrity_failure > count_mismatch > dangling/unverifiable。
// 全部命中都保留并报告，顺序仅保证确定性与优先级呈现。
func orderIssues(issues []Issue) []Issue {
	rank := map[IssueKind]int{
		KindOutOfRange:       0,
		KindIntegrityFailure: 1,
		KindCountMismatch:    2,
		KindDanglingRef:      3,
		KindRefUnverifiable:  3,
	}
	sort.SliceStable(issues, func(i, j int) bool {
		ri, rj := rank[issues[i].Kind], rank[issues[j].Kind]
		if ri != rj {
			return ri < rj
		}
		if issues[i].Chunk != issues[j].Chunk {
			if issues[i].Chunk.Type != issues[j].Chunk.Type {
				return issues[i].Chunk.Type < issues[j].Chunk.Type
			}
			return issues[i].Chunk.Chunk < issues[j].Chunk.Chunk
		}
		return issues[i].Reason < issues[j].Reason
	})
	return issues
}
