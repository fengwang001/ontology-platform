package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveExpander 是严格按题面规则对全部文档逐个扫描的朴素实现，
// 与生产实现互为对拍参照。
type naiveExpander struct {
	docs   map[string]map[string]struct{}
	clicks []clickRecord
	t      int64
}

func newNaive() *naiveExpander {
	return &naiveExpander{docs: map[string]map[string]struct{}{}}
}

func naiveValidTerm(s string) bool {
	return s != "" && !strings.ContainsRune(s, ' ')
}

func (n *naiveExpander) index(docID string, terms []string) error {
	if docID == "" || len(terms) == 0 {
		return ErrInvalidArgument
	}
	set := map[string]struct{}{}
	for _, term := range terms {
		if !naiveValidTerm(term) {
			return ErrInvalidArgument
		}
		set[term] = struct{}{}
	}
	if _, ok := n.docs[docID]; ok {
		return ErrDuplicateDoc
	}
	n.docs[docID] = set
	return nil
}

func (n *naiveExpander) remove(docID string) error {
	if _, ok := n.docs[docID]; !ok {
		return ErrDocNotFound
	}
	delete(n.docs, docID)
	return nil
}

func (n *naiveExpander) termExists(term string) bool {
	for _, set := range n.docs {
		if _, ok := set[term]; ok {
			return true
		}
	}
	return false
}

func (n *naiveExpander) pick(term string) error {
	if !naiveValidTerm(term) {
		return ErrInvalidArgument
	}
	if !n.termExists(term) {
		return ErrTermNotFound
	}
	c := n.t
	for _, r := range n.clicks {
		if r.c == c && r.term == term {
			return nil
		}
	}
	n.clicks = append(n.clicks, clickRecord{term: term, c: c})
	return nil
}

func (n *naiveExpander) expand(query string, maxExp int) (ExpandResult, error) {
	complete, prefix, hasPrefix := parseQuery(query)
	nTerms := len(complete)
	if hasPrefix {
		nTerms++
	}
	if nTerms == 0 {
		return ExpandResult{}, ErrEmptyQuery
	}
	if nTerms > 8 {
		return ExpandResult{}, ErrQueryTooLong
	}
	if maxExp < 1 || maxExp > 1000 {
		return ExpandResult{}, ErrInvalidMaxExp
	}

	n.t++
	t := n.t

	need := map[string]struct{}{}
	for _, w := range complete {
		need[w] = struct{}{}
	}
	var condIDs []string
	condSets := map[string]map[string]struct{}{}
	for docID, set := range n.docs {
		ok := true
		for w := range need {
			if _, has := set[w]; !has {
				ok = false
				break
			}
		}
		if ok {
			condIDs = append(condIDs, docID)
			condSets[docID] = set
		}
	}
	sort.Strings(condIDs)

	empty := ExpandResult{Candidates: []Candidate{}, Hits: []string{}}
	if !hasPrefix {
		empty.Hits = append(empty.Hits, condIDs...)
		return empty, nil
	}

	condDF := map[string]int{}
	for _, docID := range condIDs {
		seen := map[string]struct{}{}
		for term := range condSets[docID] {
			if strings.HasPrefix(term, prefix) {
				seen[term] = struct{}{}
			}
		}
		for term := range seen {
			condDF[term]++
		}
	}

	active := map[string]int{}
	for _, r := range n.clicks {
		if t-r.c <= 3 {
			active[r.term]++
		}
	}

	var allTerms []string
	for term, df := range condDF {
		if df > 0 {
			allTerms = append(allTerms, term)
		}
	}
	sort.Slice(allTerms, func(i, j int) bool {
		si := condDF[allTerms[i]] + 2*active[allTerms[i]]
		sj := condDF[allTerms[j]] + 2*active[allTerms[j]]
		if si != sj {
			return si > sj
		}
		return allTerms[i] < allTerms[j]
	})

	truncated := len(allTerms) > maxExp
	kept := allTerms
	if truncated {
		kept = allTerms[:maxExp]
	}
	out := ExpandResult{Candidates: []Candidate{}, Truncated: truncated, Hits: []string{}}
	keptSet := map[string]struct{}{}
	for _, term := range kept {
		out.Candidates = append(out.Candidates, Candidate{
			Term:   term,
			CondDF: condDF[term],
			Score:  condDF[term] + 2*active[term],
		})
		keptSet[term] = struct{}{}
	}
	for _, docID := range condIDs {
		for term := range keptSet {
			if _, has := condSets[docID][term]; has {
				out.Hits = append(out.Hits, docID)
				break
			}
		}
	}
	return out, nil
}

type fuzzOp struct {
	kind   string // index / remove / pick / expand
	docID  string
	terms  []string
	term   string
	query  string
	maxExp int
}

var fuzzWords = []string{
	"", "a", "ap", "ape", "apex", "apple", "apply", "app",
	"b", "bar", "bark", "cat", "dog", "do", "M", "Ta",
	"x1", "x2", "two words", // 含空格，用于制造非法参数
}

func randomTerms(rng *rand.Rand) []string {
	n := 1 + rng.Intn(4)
	out := make([]string, n)
	for i := range out {
		out[i] = fuzzWords[rng.Intn(len(fuzzWords))]
	}
	return out
}

func randomQuery(rng *rand.Rand) string {
	n := rng.Intn(10) // 0..9 个词，偶尔超过 8
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fuzzWords[rng.Intn(len(fuzzWords)-1)+1]) // 不含空串
	}
	q := strings.Join(parts, strings.Repeat(" ", 1+rng.Intn(2)))
	if rng.Intn(2) == 0 && len(parts) > 0 {
		q += strings.Repeat(" ", 1+rng.Intn(2))
	}
	if n == 0 {
		q = strings.Repeat(" ", rng.Intn(3)) // 偶尔空查询
	}
	return q
}

// TestFuzzAgainstNaive 对 2000 组随机操作序列做对拍。
// 每组日志打印输入、两侧输出与判定依据；不一致时立即失败。
func TestFuzzAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		real := New()
		naive := newNaive()

		nOps := 1 + rng.Intn(30)
		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d ops=%d\n", seq, nOps)

		for opi := 0; opi < nOps; opi++ {
			op := fuzzOp{kind: []string{"index", "index", "remove", "pick", "expand"}[rng.Intn(5)]}
			switch op.kind {
			case "index":
				op.docID = []string{"d1", "d2", "d3", "d4", "", "d1"}[rng.Intn(6)]
				op.terms = randomTerms(rng)
				errR := real.Index(op.docID, op.terms)
				errN := naive.index(op.docID, append([]string(nil), op.terms...))
				fmt.Fprintf(&log, " [%d] Index(%q,%v) -> %v\n", opi, op.docID, op.terms, errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seq=%d op=%d Index error mismatch real=%v naive=%v\n%s", seq, opi, errR, errN, log.String())
				}
			case "remove":
				op.docID = []string{"d1", "d2", "d3", "nope"}[rng.Intn(4)]
				errR := real.Remove(op.docID)
				errN := naive.remove(op.docID)
				fmt.Fprintf(&log, " [%d] Remove(%q) -> %v\n", opi, op.docID, errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seq=%d op=%d Remove mismatch %v vs %v\n%s", seq, opi, errR, errN, log.String())
				}
			case "pick":
				op.term = fuzzWords[rng.Intn(len(fuzzWords))]
				errR := real.Pick(op.term)
				errN := naive.pick(op.term)
				fmt.Fprintf(&log, " [%d] Pick(%q) -> %v\n", opi, op.term, errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seq=%d op=%d Pick mismatch %v vs %v\n%s", seq, opi, errR, errN, log.String())
				}
			case "expand":
				op.query = randomQuery(rng)
				op.maxExp = []int{0, 1, 2, 3, 10, 1000, 1001}[rng.Intn(7)]
				rR, errR := real.Expand(op.query, op.maxExp)
				rN, errN := naive.expand(op.query, op.maxExp)
				fmt.Fprintf(&log, " [%d] Expand(%q,%d) -> {cands=%v trunc=%v hits=%v} err=%v\n",
					opi, op.query, op.maxExp, rR.Candidates, rR.Truncated, rR.Hits, errR)
				if !sameErr(errR, errN) {
					t.Fatalf("seq=%d op=%d Expand error mismatch %v vs %v\n%s", seq, opi, errR, errN, log.String())
				}
				if errR == nil {
					if !equalResult(rR, rN) {
						t.Fatalf("seq=%d op=%d Expand result mismatch\n real=%+v\nnaive=%+v\n判定: 逐字段比较 Candidates/Truncated/Hits\n%s",
							seq, opi, rR, rN, log.String())
					}
				}
			}
		}

		// 序列结束后再校验内部 T 一致（通过可观测行为：两侧各跑一次相同 Expand）。
		probe := "ap"
		rR, _ := real.Expand(probe, 100)
		rN, _ := naive.expand(probe, 100)
		fmt.Fprintf(&log, " probe Expand(%q) real=%+v naive=%+v => 判定: 得分中的有效点选窗口一致\n", probe, rR, rN)
		if !equalResult(rR, rN) {
			t.Fatalf("seq=%d probe mismatch\n real=%+v\nnaive=%+v\n%s", seq, rR, rN, log.String())
		}

		// 日志中打印输入、输出与判定依据（-v 时可见）。
		t.Logf("seq=%d 判定: %d 个操作及最终 probe 的错误标识与结果字段全部一致\n%s", seq, nOps, log.String())
	}
}

func sameErr(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Error() == b.Error()
}

func equalResult(a, b ExpandResult) bool {
	if a.Truncated != b.Truncated {
		return false
	}
	if len(a.Candidates) != len(b.Candidates) {
		return false
	}
	for i := range a.Candidates {
		if a.Candidates[i] != b.Candidates[i] {
			return false
		}
	}
	if len(a.Hits) != len(b.Hits) {
		return false
	}
	for i := range a.Hits {
		if a.Hits[i] != b.Hits[i] {
			return false
		}
	}
	return true
}

// TestConcurrentExercises 在 -race 下验证并发调用的安全性。
func TestConcurrentExercises(t *testing.T) {
	e := New()
	done := make(chan struct{})
	for w := 0; w < 8; w++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				doc := fmt.Sprintf("d%d-%d", id, i%5)
				_ = e.Index(doc, []string{"apple", "ape", "ap"})
				_, _ = e.Expand("ap", 10)
				_ = e.Pick("apple")
				_ = e.Remove(doc)
			}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
}
