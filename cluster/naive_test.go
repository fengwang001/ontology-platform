package cluster

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/token"
)

// naiveTpl 是朴素模拟器里的模板，逐步照规格手写。
type naiveTpl struct {
	id    int64
	words []string
	count int64
	gen   int64
}

// naiveSim 独立于生产代码重新实现同一份规则，仅用于差分对照。
type naiveSim struct {
	theta    int
	tmax     int
	gen      int64
	overflow map[string]int64
	leaves   map[string]map[string][]*naiveTpl // tenant -> leafKey -> tpls
	nextIDs  map[string]int64
}

func newNaive(theta, tmax int) *naiveSim {
	return &naiveSim{
		theta:    theta,
		tmax:     tmax,
		overflow: map[string]int64{},
		leaves:   map[string]map[string][]*naiveTpl{},
		nextIDs:  map[string]int64{},
	}
}

func leafK(tok []string) string { return fmt.Sprintf("%d\x00%s", len(tok), tok[0]) }

// ingest 模拟一次归并并返回与生产 Result 对齐的字段及判定理由。
func (s *naiveSim) ingest(tenant, msg string) (id int64, created, overflow bool, reason string) {
	tok, _ := token.Tokenize(msg)
	leafs := s.leaves[tenant]
	if leafs == nil {
		leafs = map[string][]*naiveTpl{}
		s.leaves[tenant] = leafs
	}
	key := leafK(tok)
	var best *naiveTpl
	bestEq, bestWild := -1, 0
	for _, tp := range leafs[key] {
		eq, wild := 0, 0
		for i := range tok {
			if tp.words[i] == token.Wildcard {
				eq, wild = eq+1, wild+1
			} else if tp.words[i] == tok[i] {
				eq++
			}
		}
		if eq*100 < len(tok)*s.theta {
			continue
		}
		if best == nil || eq > bestEq || (eq == bestEq && wild < bestWild) ||
			(eq == bestEq && wild == bestWild && tp.id < best.id) {
			best, bestEq, bestWild = tp, eq, wild
		}
	}
	if best != nil {
		for i := range tok {
			if best.words[i] != token.Wildcard && best.words[i] != tok[i] {
				best.words[i] = token.Wildcard
			}
		}
		best.count++
		return best.id, false, false,
			fmt.Sprintf("命中 id=%d eq=%d/%d 通配=%d", best.id, bestEq, len(tok), bestWild)
	}
	createdCount := 0
	for _, ls := range leafs {
		createdCount += len(ls)
	}
	if createdCount >= s.tmax {
		s.overflow[tenant]++
		return 0, false, true, fmt.Sprintf("叶子内无合格模板且模板数=%d 达上限→溢出桶", createdCount)
	}
	s.nextIDs[tenant]++
	tp := &naiveTpl{id: s.nextIDs[tenant], words: append([]string(nil), tok...), count: 1, gen: s.gen}
	leafs[key] = append(leafs[key], tp)
	return tp.id, true, false, fmt.Sprintf("叶子 (%s) 无合格模板→新建 id=%d gen=%d", key, tp.id, tp.gen)
}

func (s *naiveSim) setTheta(theta int) { s.theta = theta; s.gen++ }

func (s *naiveSim) snapshot(tenant string) []naiveTpl {
	var out []naiveTpl
	for _, ls := range s.leaves[tenant] {
		for _, tp := range ls {
			out = append(out, *tp)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].id > out[j].id; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func rngSource() *rand.Rand { return rand.New(rand.NewSource(20261003)) }

func TestConcurrentIngestAndSetTheta(t *testing.T) {
	m, _ := New(40, 100000, 100)
	vocab := []string{"open", "close", "read", "write", "ok", "fail", "x", "y"}
	msgs := make([]string, 80)
	for i := range msgs {
		n := 2 + i%4
		ws := make([]string, n)
		for j := range ws {
			ws[j] = vocab[(i+j)%len(vocab)]
		}
		msgs[i] = strings.Join(ws, " ")
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g + 1)))
			for k := 0; k < 300; k++ {
				tn := []string{"a", "b", "c", "d"}[rng.Intn(4)]
				if _, err := m.Ingest(tn, msgs[rng.Intn(len(msgs))]); err != nil {
					t.Errorf("并发 Ingest 错误: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := 0; k < 50; k++ {
			theta := 1 + (k % 100)
			if err := m.SetTheta(theta); err != nil {
				t.Errorf("SetTheta: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// 不变量：id 严格递增；所有租户 Σcount + overflow 之和 == 接受总数。
	total := int64(0)
	for _, tn := range []string{"a", "b", "c", "d"} {
		infos, overflow, err := m.Snapshot(tn)
		if err != nil {
			t.Fatal(err)
		}
		var sum, prev int64
		for _, x := range infos {
			sum += x.Count
			if x.ID <= prev {
				t.Fatalf("id 必须严格递增: %d after %d", x.ID, prev)
			}
			prev = x.ID
		}
		if sum < int64(len(infos)) {
			t.Fatalf("租户 %s 计数异常", tn)
		}
		total += sum + overflow
	}
	if total != 8*300 {
		t.Fatalf("接受总数应=%d, got %d", 8*300, total)
	}
	t.Logf("并发交错通过: 接受日志总数=%d, Gen=%d, id 严格递增", total, m.Gen())
}
