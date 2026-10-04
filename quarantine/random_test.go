package quarantine

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 本文件把 Planner 与一份按规则逐步写成的朴素模拟做对照：
// 1500 组随机操作序列，逐操作比较分片、判定与错误。

type nMark int

const (
	nClean nMark = iota
	nFlaky
	nBroken
)

func (m nMark) String() string {
	switch m {
	case nClean:
		return "Clean"
	case nFlaky:
		return "Flaky"
	}
	return "Broken"
}

type nBuild struct {
	tests    []string
	snap     map[string]bool
	ests     map[string]int64
	tries    map[string]int
	mark     map[string]nMark
	has      map[string]bool
	ms       map[string]int64
	finished bool
}

// naive 是按规则逐条写成的朴素模拟，与实现互不共享代码。
type naive struct {
	W, F, P, R, D int
	samples       map[string][]int64
	window        map[string][]nMark
	quar          map[string]bool
	streak        map[string]int
	builds        map[string]*nBuild
}

func newNaive(W, F, P, R, D int) *naive {
	return &naive{
		W: W, F: F, P: P, R: R, D: D,
		samples: map[string][]int64{},
		window:  map[string][]nMark{},
		quar:    map[string]bool{},
		streak:  map[string]int{},
		builds:  map[string]*nBuild{},
	}
}

func (nv *naive) estOf(test string) (int64, bool) {
	s := nv.samples[test]
	if len(s) == 0 {
		return 0, false
	}
	var sum int64
	for _, v := range s {
		sum += v
	}
	n := int64(len(s))
	return (sum + n - 1) / n, true
}

func (nv *naive) plan(build string, tests []string, n int) ([][]string, error) {
	if build == "" || len(tests) < 1 || len(tests) > 10_000 || n < 1 || n > 256 {
		return nil, ErrInvalidParam
	}
	seen := map[string]bool{}
	for _, tn := range tests {
		if tn == "" || seen[tn] {
			return nil, ErrInvalidParam
		}
		seen[tn] = true
	}
	if _, ok := nv.builds[build]; ok {
		return nil, ErrBuildExists
	}
	snap := map[string]bool{}
	ests := map[string]int64{}
	has := map[string]bool{}
	var pool []int64
	for _, tn := range tests {
		q := nv.quar[tn]
		snap[tn] = q
		if e, ok := nv.estOf(tn); ok {
			ests[tn] = e
			has[tn] = true
			if !q {
				pool = append(pool, e)
			}
		}
	}
	def := int64(1)
	if len(pool) > 0 {
		sort.Slice(pool, func(i, j int) bool { return pool[i] < pool[j] })
		def = pool[(len(pool)+1)/2-1]
	}
	for _, tn := range tests {
		if !has[tn] {
			ests[tn] = def
		}
	}
	var reg, quar []string
	for _, tn := range tests {
		if snap[tn] {
			quar = append(quar, tn)
		} else {
			reg = append(reg, tn)
		}
	}
	sort.Slice(reg, func(i, j int) bool {
		if ests[reg[i]] != ests[reg[j]] {
			return ests[reg[i]] > ests[reg[j]]
		}
		return reg[i] < reg[j]
	})
	shards := make([][]string, n)
	sums := make([]int64, n)
	for _, tn := range reg {
		best := 0
		for i := 1; i < n; i++ {
			if sums[i] < sums[best] {
				best = i
			}
		}
		shards[best] = append(shards[best], tn)
		sums[best] += ests[tn]
	}
	if len(quar) > 0 {
		sort.Strings(quar)
		shards = append(shards, quar)
	}
	nv.builds[build] = &nBuild{
		tests: append([]string(nil), tests...),
		snap:  snap,
		ests:  ests,
		tries: map[string]int{},
		mark:  map[string]nMark{},
		has:   map[string]bool{},
		ms:    map[string]int64{},
	}
	return shards, nil
}

func (nv *naive) report(build, test string, pass bool, ms int64) error {
	if ms < 0 || ms > 1_000_000_000 {
		return ErrInvalidParam
	}
	b, ok := nv.builds[build]
	if !ok {
		return ErrBuildNotFound
	}
	if b.finished {
		return ErrBuildFinished
	}
	if _, in := b.snap[test]; !in {
		return ErrTestNotInPlan
	}
	if b.has[test] {
		return ErrState
	}
	b.tries[test]++
	switch {
	case pass && b.tries[test] == 1:
		b.has[test], b.mark[test], b.ms[test] = true, nClean, ms
	case pass:
		b.has[test], b.mark[test], b.ms[test] = true, nFlaky, ms
	case b.tries[test] > nv.R:
		b.has[test], b.mark[test] = true, nBroken
	}
	return nil
}

// finish 返回判定、判定依据与错误。
func (nv *naive) finish(build string) (Verdict, string, error) {
	b, ok := nv.builds[build]
	if !ok {
		return Passed, "", ErrBuildNotFound
	}
	if b.finished {
		return Passed, "", ErrBuildFinished
	}
	for _, tn := range b.tests {
		if !b.has[tn] {
			return Passed, "", ErrIncomplete
		}
	}
	verdict := Passed
	reason := "快照中未隔离用例均无 Broken"
	for _, tn := range b.tests {
		if !b.snap[tn] && b.mark[tn] == nBroken {
			verdict = Failed
			reason = fmt.Sprintf("快照中未隔离用例 %s 为 Broken", tn)
			break
		}
	}
	b.finished = true
	sorted := append([]string(nil), b.tests...)
	sort.Strings(sorted)
	for _, tn := range sorted {
		m := b.mark[tn]
		if m != nBroken {
			nv.samples[tn] = append(nv.samples[tn], b.ms[tn])
			if len(nv.samples[tn]) > nv.D {
				nv.samples[tn] = nv.samples[tn][len(nv.samples[tn])-nv.D:]
			}
		}
		if !nv.quar[tn] {
			nv.window[tn] = append(nv.window[tn], m)
			if len(nv.window[tn]) > nv.W {
				nv.window[tn] = nv.window[tn][len(nv.window[tn])-nv.W:]
			}
			flaky := 0
			for _, mm := range nv.window[tn] {
				if mm == nFlaky {
					flaky++
				}
			}
			if flaky >= nv.F {
				nv.quar[tn] = true
				nv.streak[tn] = 0
			}
		} else if m == nClean {
			nv.streak[tn]++
			if nv.streak[tn] >= nv.P {
				nv.quar[tn] = false
				nv.streak[tn] = 0
				nv.window[tn] = nil
			}
		} else {
			nv.streak[tn] = 0
		}
	}
	return verdict, reason, nil
}
func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// shardProblem 校验分片不变量：每个用例恰出现一次、隔离分片有序、
// 任意两个常规分片 est 总和之差不超过单个用例最大 est。
func shardProblem(tests []string, n int, shards [][]string, ests map[string]int64) string {
	if len(shards) != n && len(shards) != n+1 {
		return fmt.Sprintf("shard count = %d, want %d or %d", len(shards), n, n+1)
	}
	count := map[string]int{}
	for _, sh := range shards {
		for _, tn := range sh {
			count[tn]++
		}
	}
	for _, tn := range tests {
		if count[tn] != 1 {
			return fmt.Sprintf("test %s appears %d times in shards %v", tn, count[tn], shards)
		}
	}
	if len(shards) == n+1 && !sort.StringsAreSorted(shards[n]) {
		return fmt.Sprintf("quarantine shard %v not sorted", shards[n])
	}
	sums := make([]int64, n)
	var maxEst int64
	any := false
	for i := 0; i < n; i++ {
		for _, tn := range shards[i] {
			sums[i] += ests[tn]
			if ests[tn] > maxEst {
				maxEst = ests[tn]
			}
			any = true
		}
	}
	if !any {
		return ""
	}
	lo, hi := sums[0], sums[0]
	for _, s := range sums {
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	if hi-lo > maxEst {
		return fmt.Sprintf("shard sums %v differ by %d > max est %d", sums, hi-lo, maxEst)
	}
	return ""
}

// TestRandomAgainstNaive 用 1500 组随机操作序列把 Planner（两份实例互证
// 重放确定性）与朴素模拟逐步对照，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s)*7919 + 1))
		W := 1 + rng.Intn(8)
		F := 1 + rng.Intn(W)
		P := 1 + rng.Intn(4)
		R := rng.Intn(4)
		D := 1 + rng.Intn(5)
		numTests := 2 + rng.Intn(9)
		all := make([]string, numTests)
		for i := range all {
			all[i] = fmt.Sprintf("t%d", i)
		}
		p1, err := New(W, F, P, R, D)
		if err != nil {
			t.Fatalf("seq %d: New: %v", s, err)
		}
		p2, err := New(W, F, P, R, D)
		if err != nil {
			t.Fatalf("seq %d: New: %v", s, err)
		}
		nv := newNaive(W, F, P, R, D)
		var oplog []string
		logf := func(format string, args ...any) {
			oplog = append(oplog, fmt.Sprintf(format, args...))
		}
		fail := func(format string, args ...any) {
			t.Helper()
			msg := fmt.Sprintf(format, args...)
			t.Fatalf("seq %d (W=%d F=%d P=%d R=%d D=%d): %s\nop log:\n%s",
				s, W, F, P, R, D, msg, strings.Join(oplog, "\n"))
		}
		var open []string
		var verdicts []string
		finishBuild := func(b string) {
			v1, e1 := p1.Finish(b)
			v2, e2 := p2.Finish(b)
			nvv, reason, ne := nv.finish(b)
			logf("Finish(%s) -> verdict=%v err=%v 依据: %s", b, nvv, ne, reason)
			if !sameErr(e1, ne) || !sameErr(e2, ne) {
				fail("Finish(%s) err mismatch: impl=%v/%v naive=%v", b, e1, e2, ne)
			}
			if e1 == nil {
				if v1 != nvv || v2 != nvv {
					fail("Finish(%s) verdict mismatch: impl=%v/%v naive=%v", b, v1, v2, nvv)
				}
				verdicts = append(verdicts, fmt.Sprintf("%s=%v", b, nvv))
			}
		}
		report := func(b, test string, pass bool, ms int64) {
			e1 := p1.Report(b, test, pass, ms)
			e2 := p2.Report(b, test, pass, ms)
			ne := nv.report(b, test, pass, ms)
			logf("Report(%s, %s, %v, %d) -> err=%v", b, test, pass, ms, ne)
			if !sameErr(e1, ne) || !sameErr(e2, ne) {
				fail("Report(%s,%s,%v,%d) err mismatch: impl=%v/%v naive=%v",
					b, test, pass, ms, e1, e2, ne)
			}
		}
		drain := func(b string) {
			nb := nv.builds[b]
			for _, tn := range nb.tests {
				for !nb.has[tn] {
					report(b, tn, rng.Intn(3) > 0, int64(rng.Intn(30)))
				}
			}
		}
		numBuilds := 3 + rng.Intn(5)
		buildIdx := 0
		for steps := 0; (buildIdx < numBuilds || len(open) > 0) && steps < 300; steps++ {
			if buildIdx < numBuilds && (len(open) == 0 || rng.Intn(3) > 0) {
				build := fmt.Sprintf("b%d", buildIdx)
				buildIdx++
				var subset []string
				for _, tn := range all {
					if rng.Intn(2) == 0 {
						subset = append(subset, tn)
					}
				}
				if len(subset) == 0 {
					subset = []string{all[rng.Intn(len(all))]}
				}
				n := 1 + rng.Intn(4)
				sh1, e1 := p1.Plan(build, subset, n)
				sh2, e2 := p2.Plan(build, subset, n)
				nsh, ne := nv.plan(build, subset, n)
				logf("Plan(%s, %v, %d) -> shards=%v err=%v", build, subset, n, nsh, ne)
				if !sameErr(e1, ne) || !sameErr(e2, ne) {
					fail("Plan(%s) err mismatch: impl=%v/%v naive=%v", build, e1, e2, ne)
				}
				if e1 == nil {
					if !reflect.DeepEqual(sh1, nsh) || !reflect.DeepEqual(sh2, nsh) {
						fail("Plan(%s) shards mismatch:\nimpl=%v\nimpl2=%v\nnaive=%v",
							build, sh1, sh2, nsh)
					}
					if prob := shardProblem(subset, n, sh1, nv.builds[build].ests); prob != "" {
						fail("Plan(%s) invariant: %s", build, prob)
					}
					open = append(open, build)
				}
				continue
			}
			b := open[rng.Intn(len(open))]
			if rng.Intn(4) < 3 {
				test := all[rng.Intn(len(all))]
				ms := int64(rng.Intn(30))
				if rng.Intn(25) == 0 {
					ms = -1 // 偶发非法参数
				}
				report(b, test, rng.Intn(3) > 0, ms)
				continue
			}
			if rng.Intn(4) > 0 {
				drain(b) // 多数情况下先补全再 Finish，偶发直接 Finish 触发未完成
			}
			finishBuild(b)
			if nv.builds[b].finished {
				for i, x := range open {
					if x == b {
						open = append(open[:i], open[i:]...)
						break
					}
				}
			}
		}
		for _, b := range open {
			drain(b)
			finishBuild(b)
		}
		t.Logf("seq=%d W=%d F=%d P=%d R=%d D=%d tests=%d verdicts=%s",
			s, W, F, P, R, D, numTests, strings.Join(verdicts, ","))
		if s < 3 {
			t.Logf("seq %d 完整操作日志:\n%s", s, strings.Join(oplog, "\n"))
		}
	}
}
