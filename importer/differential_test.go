package importer_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"ontology/importer"
	"ontology/importer/naive"
)

func diffValidator(ent importer.Entry) error {
	if strings.Contains(ent.Payload, "INVALID") {
		return fmt.Errorf("entry %q is invalid", ent.ID)
	}
	return nil
}

// scenario 是一次随机生成的导入：若干块、随机乱序、随机注入重复到达。
type scenario struct {
	maxChunks int
	steps     []importer.Chunk // 按到达顺序排列，含重复与冲突注入
	universe  []string         // 所有出现过的条目 ID（含冲突注入引入的）
}

// generate 随机生成块划分、跨块引用（含悬空引用与环）、乱序到达与重复注入。
// allowConflict 控制是否注入内容不一致的重复块（并发对拍时关闭，
// 因为并发下哪个版本先到达本身不确定）。
func generate(rng *rand.Rand, allowConflict bool) scenario {
	numChunks := 1 + rng.IntN(6)
	numEntries := numChunks * (1 + rng.IntN(4))

	ids := make([]string, numEntries)
	for i := range ids {
		ids[i] = fmt.Sprintf("e%d", i)
	}
	// 随机划分条目到块。
	chunks := make([][]importer.Entry, numChunks)
	perm := rng.Perm(numEntries)
	for i, idx := range perm {
		target := i % numChunks
		chunks[target] = append(chunks[target], makeEntry(rng, ids[idx], ids, numEntries))
	}

	blocks := make([]importer.Chunk, numChunks)
	for s := range blocks {
		blocks[s] = importer.Chunk{JobID: "job", Seq: s, Entries: chunks[s]}
	}

	// 乱序 + 重复注入。
	var steps []importer.Chunk
	for _, b := range blocks {
		steps = append(steps, b)
		if rng.Float64() < 0.35 {
			steps = append(steps, b) // 内容一致的重复到达
		}
		if allowConflict && rng.Float64() < 0.25 {
			steps = append(steps, mutate(rng, b)) // 内容不一致的重复到达
		}
	}
	rng.Shuffle(len(steps), func(i, j int) { steps[i], steps[j] = steps[j], steps[i] })

	seen := make(map[string]bool)
	var universe []string
	for _, c := range steps {
		for _, ent := range c.Entries {
			if !seen[ent.ID] {
				seen[ent.ID] = true
				universe = append(universe, ent.ID)
			}
		}
	}
	return scenario{maxChunks: numChunks, steps: steps, universe: universe}
}

func makeEntry(rng *rand.Rand, id string, ids []string, numEntries int) importer.Entry {
	ent := importer.Entry{ID: id}
	if rng.Float64() < 0.15 {
		ent.Payload = "INVALID"
	}
	numRefs := rng.IntN(3)
	for k := 0; k < numRefs; k++ {
		if rng.Float64() < 0.8 {
			ent.References = append(ent.References, ids[rng.IntN(numEntries)])
		} else {
			ent.References = append(ent.References, fmt.Sprintf("ghost-%d", rng.IntN(3)))
		}
	}
	return ent
}

// mutate 生成同序号但内容不同的块：修改条目载荷或追加新条目。
func mutate(rng *rand.Rand, c importer.Chunk) importer.Chunk {
	out := importer.Chunk{JobID: c.JobID, Seq: c.Seq}
	out.Entries = append(out.Entries, c.Entries...)
	if len(out.Entries) > 0 && rng.Float64() < 0.5 {
		out.Entries[rng.IntN(len(out.Entries))].Payload += "-mutated"
	} else {
		out.Entries = append(out.Entries, importer.Entry{ID: fmt.Sprintf("mut-%d-%d", c.Seq, rng.IntN(1000))})
	}
	return out
}

func compareWithNaive(t *testing.T, e *importer.Engine, m *naive.Model, sc scenario) {
	t.Helper()
	want := m.Finalize()
	for _, id := range sc.universe {
		got := e.QueryEntry("job", id)
		exp, ok := want[id]
		if !ok {
			t.Fatalf("entry %s missing in naive result", id)
		}
		if got.Status != exp.Status || got.Category != exp.Category {
			t.Fatalf("entry %s: engine=(%v,%v) naive=(%v,%v)",
				id, got.Status, got.Category, exp.Status, exp.Category)
		}
	}
}

// 对拍：随机乱序 + 重复注入（含内容不一致）下，引擎的最终判定必须与
// “等待全部块到齐再统一处理”的朴素模型完全一致。
func TestDifferentialRandomized(t *testing.T) {
	for seed := uint64(0); seed < 3000; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
		sc := generate(rng, true)

		e := importer.NewEngine(importer.WithValidator(diffValidator))
		if err := e.CreateJob("job", sc.maxChunks); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		m := naive.New(sc.maxChunks, diffValidator)
		for _, c := range sc.steps {
			e.SubmitChunk(c)
			m.Submit(c)
		}
		if err := compareWithNaiveSeed(e, m, sc); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func compareWithNaiveSeed(e *importer.Engine, m *naive.Model, sc scenario) error {
	want := m.Finalize()
	for _, id := range sc.universe {
		got := e.QueryEntry("job", id)
		exp, ok := want[id]
		if !ok {
			return fmt.Errorf("entry %s missing in naive result", id)
		}
		if got.Status != exp.Status || got.Category != exp.Category {
			return fmt.Errorf("entry %s: engine=(%v,%v) naive=(%v,%v)",
				id, got.Status, got.Category, exp.Status, exp.Category)
		}
	}
	return nil
}

// 并发对拍：块并发到达（含内容一致的重复注入），最终结果必须与朴素
// 模型一致，即并发处理等价于某个确定的串行顺序。
func TestDifferentialConcurrent(t *testing.T) {
	for seed := uint64(1000); seed < 1400; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed^0x5bd1e995))
		sc := generate(rng, false)

		e := importer.NewEngine(importer.WithValidator(diffValidator))
		if err := e.CreateJob("job", sc.maxChunks); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		m := naive.New(sc.maxChunks, diffValidator)

		var wg sync.WaitGroup
		for _, c := range sc.steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.SubmitChunk(c)
			}()
			m.Submit(c)
		}
		wg.Wait()
		if err := compareWithNaiveSeed(e, m, sc); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}
