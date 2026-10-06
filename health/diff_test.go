package health

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// model 抽象两个引擎的相同入口，便于对同一操作序列做差分。
type model interface {
	AddCode(CodeInput) error
	ChangeParent(string, string) error
	RegisterPolicy(PolicyInput) error
	SubmitClaim(ClaimInput) (*ClaimResult, error)
	ArchiveSnapshot(string) []string
}

type opLog struct{ b strings.Builder }

func (l *opLog) line(format string, args ...any) { fmt.Fprintf(&l.b, format+"\n", args...) }

func resultKey(r *ClaimResult) string {
	if r == nil {
		return "nil"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "payout=%d", r.Payout)
	for _, v := range r.Verdicts {
		fmt.Fprintf(&b, "|%s:%d:%s", v.Code, v.Fee, v.Reason)
	}
	return b.String()
}

// runRandom 生成随机的目录变更、保单登记与理赔序列，在两个模型上
// 以完全相同的顺序重放，逐操作比对错误、赔付与逐诊断依据，最后比对档案。
// 所有输入、输出与依据写入日志，失配时随失败信息打印。
func runRandom(t *testing.T, seed int64, ops int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var ref, sut model = NewNaiveEngine(), NewEngine()
	var log opLog

	persons := []string{"p0", "p1", "p2"}
	codePool := []string{}
	claimSeq := 0

	personDays := map[string]int64{} // 每个被保人已用到的最大右端，便于制造相接续保
	for i := 0; i < ops; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2: // 目录变更（约 30%）
			if rng.Intn(6) == 0 && len(codePool) > 1 {
				code := codePool[rng.Intn(len(codePool))]
				var parent string
				if rng.Intn(2) == 0 && len(codePool) > 1 {
					for {
						if p := codePool[rng.Intn(len(codePool))]; p != code {
							parent = p
							break
						}
					}
				}
				log.line("ChangeParent code=%s parent=%q", code, parent)
				e1 := ref.ChangeParent(code, parent)
				e2 := sut.ChangeParent(code, parent)
				log.line("  -> ref=%v sut=%v", e1, e2)
				if fmt.Sprint(e1) != fmt.Sprint(e2) {
					t.Fatalf("seed=%d ChangeParent(%s,%s) 不一致: %v vs %v\n%s", seed, code, parent, e1, e2, log.b.String())
				}
				continue
			}
			code := fmt.Sprintf("c%d", len(codePool))
			var parent string
			if len(codePool) > 0 && rng.Intn(2) == 0 {
				parent = codePool[rng.Intn(len(codePool))]
			}
			acc := rng.Intn(5) == 0
			in := CodeInput{Code: code, Parent: parent, Accident: acc}
			log.line("AddCode %+v", in)
			e1 := ref.AddCode(in)
			e2 := sut.AddCode(in)
			log.line("  -> ref=%v sut=%v", e1, e2)
			if fmt.Sprint(e1) != fmt.Sprint(e2) {
				t.Fatalf("seed=%d AddCode 不一致: %v vs %v\n%s", seed, e1, e2, log.b.String())
			}
			if e1 == nil {
				codePool = append(codePool, code)
			}
		case 3, 4, 5: // 保单登记（约 30%）
			person := persons[rng.Intn(len(persons))]
			end := personDays[person]
			start := end
			if rng.Intn(3) == 0 {
				start += int64(rng.Intn(3)) // 有时晚 0~2 天（相接/缺口/重叠混合）
			}
			newEnd := start + 10 + int64(rng.Intn(30))
			amount := int64(500 + rng.Intn(3000))
			if rng.Intn(2) == 0 {
				amount = 1000 // 常见基础保额，便于触发提额/不提额两种续保
			}
			in := PolicyInput{
				Person: person, RegisteredAt: end, Start: start, End: newEnd,
				WaitDays: int64(rng.Intn(8)), Amount: amount,
			}
			if rng.Intn(2) == 0 {
				in.End = start // 故意制造非法参数（End<=Start）
			}
			if len(codePool) > 0 && rng.Intn(2) == 0 {
				decl := codePool[rng.Intn(len(codePool))]
				if rng.Intn(5) == 0 {
					decl = "ghost" // 故意制造编码不存在
				}
				in.Declared = []string{decl}
			}
			log.line("RegisterPolicy %+v", in)
			e1 := ref.RegisterPolicy(in)
			e2 := sut.RegisterPolicy(in)
			log.line("  -> ref=%v sut=%v", e1, e2)
			if fmt.Sprint(e1) != fmt.Sprint(e2) {
				t.Fatalf("seed=%d RegisterPolicy 不一致: %v vs %v\n%s", seed, e1, e2, log.b.String())
			}
			if e1 == nil {
				personDays[person] = newEnd
			}
		default: // 理赔（约 40%）
			person := "ghost-person"
			if rng.Intn(5) < 4 {
				person = persons[rng.Intn(len(persons))]
			}
			day := int64(rng.Intn(80))
			nd := 1 + rng.Intn(3)
			diags := make([]Diagnosis, nd)
			for j := range diags {
				code := "ghost0"
				if len(codePool) > 0 {
					code = fmt.Sprintf("c%d", rng.Intn(len(codePool)+3)) // 偶发不存在编码
				}
				fee := int64(1 + rng.Intn(2000))
				if rng.Intn(12) == 0 {
					fee = 0 // 故意非法
				}
				diags[j] = Diagnosis{Code: code, Fee: fee}
			}
			id := fmt.Sprintf("clm%d", claimSeq)
			claimSeq++
			if rng.Intn(8) == 0 {
				id = "clm0" // 偶发重复号
			}
			in := ClaimInput{ID: id, Person: person, Day: day, Diagnoses: diags}
			log.line("SubmitClaim %+v", in)
			r1, e1 := ref.SubmitClaim(in)
			r2, e2 := sut.SubmitClaim(in)
			log.line("  -> ref=(%s,%v) sut=(%s,%v)", resultKey(r1), e1, resultKey(r2), e2)
			if fmt.Sprint(e1) != fmt.Sprint(e2) || resultKey(r1) != resultKey(r2) {
				t.Fatalf("seed=%d SubmitClaim 不一致:\n ref=%s,%v\n sut=%s,%v\n%s",
					seed, resultKey(r1), e1, resultKey(r2), e2, log.b.String())
			}
		}
	}
	for _, p := range persons {
		a1 := strings.Join(ref.ArchiveSnapshot(p), ",")
		a2 := strings.Join(sut.ArchiveSnapshot(p), ",")
		if a1 != a2 {
			t.Fatalf("seed=%d 被保人 %s 档案不一致: %q vs %q\n%s", seed, p, a1, a2, log.b.String())
		}
	}
	t.Logf("seed=%d 完成，操作日志：\n%s", seed, log.b.String())
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		runRandom(t, seed, 400)
	}
}

// 并发性：不同被保人的操作分区并行打到引擎；最终结果必须等价于
// 按被保人串行的某个顺序——即与同样操作的串行重放完全一致。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	build := func(parallel bool) *Engine {
		e := NewEngine()
		codes := []string{"R", "X", "Y", "Z"}
		for _, c := range codes {
			parent := ""
			if c != "R" {
				parent = "R"
			}
			if err := e.AddCode(CodeInput{Code: c, Parent: parent, Accident: c == "Z"}); err != nil {
				t.Fatal(err)
			}
		}
		playPerson := func(idx int) {
			p := fmt.Sprintf("u%d", idx)
			if err := e.RegisterPolicy(PolicyInput{Person: p, RegisteredAt: 0,
				Start: 0, End: 200, WaitDays: 5, Amount: 1000, Declared: []string{"Y"}}); err != nil {
				t.Error(err)
				return
			}
			for d := int64(0); d < 30; d++ {
				_, _ = e.SubmitClaim(ClaimInput{
					ID: fmt.Sprintf("%s-%d", p, d), Person: p, Day: d,
					Diagnoses: []Diagnosis{{Code: "X", Fee: 10}, {Code: "Z", Fee: 5}},
				})
			}
		}
		if parallel {
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) { defer wg.Done(); playPerson(i) }(i)
			}
			wg.Wait()
		} else {
			for i := 0; i < 16; i++ {
				playPerson(i)
			}
		}
		return e
	}
	serial, para := build(false), build(true)
	for i := 0; i < 16; i++ {
		p := fmt.Sprintf("u%d", i)
		got, want := para.ArchiveSnapshot(p), serial.ArchiveSnapshot(p)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("并发结果不等价于串行: %v vs %v", got, want)
		}
	}
}

// 性能证明：判定一个诊断是否除外只沿祖先链走。
// 固定祖先深度，向目录与无关档案加入大量编码，查询步数与耗时应保持不变。
func TestExclusionCostIndependentOfTotalSize(t *testing.T) {
	e := NewEngine()
	mustCode(t, e, CodeInput{Code: "L0"})
	prev := "L0"
	const depth = 5
	for i := 1; i < depth; i++ {
		code := fmt.Sprintf("L%d", i)
		mustCode(t, e, CodeInput{Code: code, Parent: prev})
		prev = code
	}
	target := prev // 祖先链深度 = depth

	e.persons["probe"] = &personState{archive: newArchive()}
	st := e.persons["probe"]
	// 10000 个互不相关的目录编码
	for i := 0; i < 10000; i++ {
		c := fmt.Sprintf("noise%d", i)
		if err := e.AddCode(CodeInput{Code: c, Parent: "L0"}); err != nil {
			t.Fatal(err)
		}
	}
	// 10000 个与 target 无祖先关系的档案编码
	for i := 0; i < 10000; i++ {
		st.archive.add(fmt.Sprintf("noise%d", i))
	}

	e.cat.mu.RLock()
	_, steps := st.archive.excluded(target, e.cat)
	e.cat.mu.RUnlock()
	if steps != depth {
		t.Fatalf("查询步数应为祖先深度 %d，实际 %d（与目录/档案规模无关）", depth, steps)
	}

	// 命中场景：祖先入档后，步数同样不超过深度。
	st.archive.add("L0")
	e.cat.mu.RLock()
	hit, stepsHit := st.archive.excluded(target, e.cat)
	e.cat.mu.RUnlock()
	if !hit || stepsHit > depth {
		t.Fatalf("祖先命中应在 %d 步内返回, hit=%v steps=%d", depth, hit, stepsHit)
	}
}

func BenchmarkExclusion(b *testing.B) {
	e := NewEngine()
	_ = e.AddCode(CodeInput{Code: "L0"})
	for i := 1; i < 6; i++ {
		_ = e.AddCode(CodeInput{Code: fmt.Sprintf("L%d", i), Parent: fmt.Sprintf("L%d", i-1)})
	}
	for i := 0; i < 50000; i++ {
		_ = e.AddCode(CodeInput{Code: fmt.Sprintf("noise%d", i), Parent: "L0"})
	}
	st := &personState{archive: newArchive()}
	for i := 0; i < 50000; i++ {
		st.archive.add(fmt.Sprintf("noise%d", i))
	}
	b.ResetTimer()
	e.cat.mu.RLock()
	for i := 0; i < b.N; i++ {
		_, _ = st.archive.excluded("L5", e.cat)
	}
	e.cat.mu.RUnlock()
}
