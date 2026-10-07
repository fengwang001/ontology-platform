package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// naiveReport 是朴素对照模型对单块的判定。
// 字段刻意与主实现的 ChunkReport 不同形，避免两边互相复制结构。
type naiveReport struct {
	Type      string
	Index     int
	OK        bool
	CountBad  bool
	Records   []Record
	DeclaredN int
	ActualN   int
}

type naiveIssue struct {
	Category   IssueKind
	Type       string
	Index      int
	TargetType string
	TargetID   string
}

// naiveResult 是独立朴素实现的判定结果。
type naiveResult struct {
	Reports      map[ChunkRef]*naiveReport
	Issues       []naiveIssue
	Aggregatable bool
}

// naiveVerify 是一份刻意“朴素”的独立实现：
//   - 块解析用流式 json.Decoder（主实现用 json.Unmarshal）；
//   - 校验和手写 io.Writer 累加（主实现用 hex/Marshal 工具函数）；
//   - 引用解析对目标块记录做线性扫描（O(N)，不用哈希索引）；
//   - 状态流转用 if/else 独立推导，不复用主实现任何判定代码。
//
// 它只与主实现共享磁盘格式约定（JSON 字段名、sha256 覆盖范围），
// 作为差分测试的“独立朴素校验模型”。
func naiveVerify(dir string, requested []string) (*naiveResult, error) {
	mf, err := naiveReadManifest(dir)
	if err != nil {
		return nil, err
	}
	res := &naiveResult{Reports: map[ChunkRef]*naiveReport{}}

	covered := map[string]bool{}
	for _, t := range mf.Types {
		covered[t] = true
	}

	// 范围外。
	reqSorted := append([]string(nil), requested...)
	sort.Strings(reqSorted)
	seen := map[string]bool{}
	for _, t := range reqSorted {
		if seen[t] {
			continue
		}
		seen[t] = true
		if !covered[t] {
			res.Issues = append(res.Issues, naiveIssue{Category: KindOutOfRange, Type: t, Index: -1})
		}
	}

	// 块级：校验与条数。
	requestedSet := map[string]bool{}
	for _, t := range reqSorted {
		requestedSet[t] = true
	}
	for _, typ := range mf.Types {
		files := mf.Chunks[typ]
		for idx, name := range files {
			rep := naiveCheckOne(filepath.Join(dir, name), typ, idx)
			res.Reports[ChunkRef{Type: rep.Type, Chunk: rep.Index}] = rep
			if !requestedSet[rep.Type] {
				continue
			}
			if !rep.OK {
				res.Issues = append(res.Issues, naiveIssue{Category: KindIntegrityFailure, Type: rep.Type, Index: rep.Index})
			} else if rep.CountBad {
				res.Issues = append(res.Issues, naiveIssue{Category: KindCountMismatch, Type: rep.Type, Index: rep.Index})
			}
		}
	}

	// 引用：仅请求且在范围内的类型的可信块；目标判定线性扫描。
	for _, typ := range reqSorted {
		if !covered[typ] {
			continue
		}
		var reps []*naiveReport
		for key, rep := range res.Reports {
			if key.Type == typ {
				reps = append(reps, rep)
			}
		}
		sort.Slice(reps, func(a, b int) bool { return reps[a].Index < reps[b].Index })
		for _, rep := range reps {
			if !rep.OK || rep.CountBad {
				continue
			}
			for _, rec := range rep.Records {
				for _, link := range rec.Refs {
					cat := naiveResolve(link, res.Reports)
					if cat != "" {
						res.Issues = append(res.Issues, naiveIssue{
							Category:   cat,
							Type:       rep.Type,
							Index:      rep.Index,
							TargetType: link.TargetType,
							TargetID:   link.TargetID,
						})
					}
				}
			}
		}
	}

	// 聚合可生成性：请求类型全部在范围内、相关块全可信、
	// 且不存在任何针对这些类型的悬空/无法校验引用。
	res.Aggregatable = naiveAggregatable(reqSorted, covered, res.Reports, res.Issues)

	sortNaiveIssues(res.Issues)
	return res, nil
}

func naiveCheckOne(path, fallbackType string, fallbackIdx int) *naiveReport {
	rep := &naiveReport{Type: fallbackType, Index: fallbackIdx}

	f, err := os.Open(path)
	if err != nil {
		return rep
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return rep
	}
	if env.Header.Type != "" {
		rep.Type = env.Header.Type
		rep.Index = env.Header.ChunkIndex
	}
	rep.DeclaredN = env.Header.DeclaredCount
	if env.Header.ChecksumAlgo != checksumAlgoSHA256 {
		return rep
	}

	raw, err := json.Marshal(env.Records)
	if err != nil {
		return rep
	}
	h := sha256.New()
	if _, err := h.Write(raw); err != nil {
		return rep
	}
	if hex.EncodeToString(h.Sum(nil)) != env.Checksum {
		return rep
	}

	rep.OK = true
	rep.ActualN = len(env.Records)
	if rep.DeclaredN != rep.ActualN {
		rep.CountBad = true
		return rep
	}
	rep.Records = env.Records
	return rep
}

// naiveResolve 线性扫描目标类型全部块的全部记录，
// 保守判定规则与规格一致：目标类型缺失或目标块不可信 => 无法校验。
func naiveResolve(link CrossTypeRef, reports map[ChunkRef]*naiveReport) IssueKind {
	var targets []*naiveReport
	for key, rep := range reports {
		if key.Type == link.TargetType {
			targets = append(targets, rep)
		}
	}
	if len(targets) == 0 {
		return KindRefUnverifiable
	}
	sort.Slice(targets, func(a, b int) bool { return targets[a].Index < targets[b].Index })
	for _, rep := range targets {
		if !rep.OK || rep.CountBad {
			return KindRefUnverifiable
		}
	}
	for _, rep := range targets {
		for _, rec := range rep.Records {
			if rec.ID == link.TargetID {
				return ""
			}
		}
	}
	return KindDanglingRef
}

func naiveAggregatable(requested []string, covered map[string]bool,
	reports map[ChunkRef]*naiveReport, issues []naiveIssue) bool {
	for _, t := range requested {
		if !covered[t] {
			return false
		}
	}
	involved := map[string]bool{}
	for _, t := range requested {
		involved[t] = true
	}
	for key, rep := range reports {
		if involved[key.Type] && (!rep.OK || rep.CountBad) {
			return false
		}
	}
	for _, is := range issues {
		if (is.Category == KindDanglingRef || is.Category == KindRefUnverifiable) && involved[is.Type] {
			return false
		}
	}
	return true
}

func naiveReadManifest(dir string) (*manifestFile, error) {
	f, err := os.Open(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var mf manifestFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

func sortNaiveIssues(issues []naiveIssue) {
	rank := map[IssueKind]int{
		KindOutOfRange:       0,
		KindIntegrityFailure: 1,
		KindCountMismatch:    2,
		KindDanglingRef:      3,
		KindRefUnverifiable:  3,
	}
	sort.SliceStable(issues, func(a, b int) bool {
		ra, rb := rank[issues[a].Category], rank[issues[b].Category]
		if ra != rb {
			return ra < rb
		}
		if issues[a].Type != issues[b].Type {
			return issues[a].Type < issues[b].Type
		}
		if issues[a].Index != issues[b].Index {
			return issues[a].Index < issues[b].Index
		}
		if issues[a].TargetType != issues[b].TargetType {
			return issues[a].TargetType < issues[b].TargetType
		}
		return issues[a].TargetID < issues[b].TargetID
	})
}
