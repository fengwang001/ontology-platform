package logkv

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
)

// modelRec 是朴素模型中的一条逻辑记录。
type modelRec struct {
	seg       uint32
	tombstone bool
	val       string
}

// naiveModel 是独立编写的朴素模型：不看盘、不扫描，只按
// “写序号最大者胜”与合并规则维护每个键的全部现存记录。
type naiveModel struct {
	seq     uint64
	active  uint32
	records map[string]map[uint64]modelRec
}

func newNaiveModel() *naiveModel {
	return &naiveModel{active: 1, records: make(map[string]map[uint64]modelRec)}
}

func (m *naiveModel) put(key, val string) {
	m.seq++
	m.add(key, modelRec{seg: m.active, val: val})
}

func (m *naiveModel) del(key string) {
	m.seq++
	m.add(key, modelRec{seg: m.active, tombstone: true})
}

func (m *naiveModel) add(key string, rec modelRec) {
	if m.records[key] == nil {
		m.records[key] = make(map[uint64]modelRec)
	}
	m.records[key][m.seq] = rec
}

func (m *naiveModel) rotate() {
	m.active++
}

// get 返回 (值, 状态, 判定依据)。
func (m *naiveModel) get(key string) (string, Status, string) {
	recs := m.records[key]
	if len(recs) == 0 {
		return "", StatusNotFound, "model: no record for key"
	}
	var maxSeq uint64
	for seq := range recs {
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	r := recs[maxSeq]
	if r.tombstone {
		return "", StatusDeleted, fmt.Sprintf("model: max seq=%d is tombstone in seg %d", maxSeq, r.seg)
	}
	return r.val, StatusFound, fmt.Sprintf("model: max seq=%d value=%q in seg %d", maxSeq, r.val, r.seg)
}

// merge 复现引擎的合并规则：集合内每键只留最大写序号记录；
// 删除标记仅当集合外无更小写序号记录时丢弃；输出段号取集合最小值。
func (m *naiveModel) merge(ids []uint32) {
	inSet := make(map[uint32]bool, len(ids))
	minID := ids[0]
	for _, id := range ids {
		inSet[id] = true
		if id < minID {
			minID = id
		}
	}
	for key, recs := range m.records {
		var maxIn uint64
		for seq, r := range recs {
			if inSet[r.seg] && seq > maxIn {
				maxIn = seq
			}
		}
		if maxIn == 0 {
			continue
		}
		kept := recs[maxIn]
		kept.seg = minID
		// 删除集合内其余记录。
		for seq, r := range recs {
			if inSet[r.seg] && seq != maxIn {
				delete(recs, seq)
			}
		}
		if kept.tombstone {
			outsideSmaller := false
			for seq, r := range recs {
				if !inSet[r.seg] && seq < maxIn {
					outsideSmaller = true
				}
			}
			if !outsideSmaller {
				delete(recs, maxIn) // 丢弃删除标记
			} else {
				recs[maxIn] = kept
			}
		} else {
			recs[maxIn] = kept
		}
		if len(recs) == 0 {
			delete(m.records, key)
		}
	}
}

// TestModelRandomOps 与朴素模型对照随机操作序列，逐条打印
// 输入、输出与判定依据。
func TestModelRandomOps(t *testing.T) {
	seeds := []int64{1, 2, 3}
	if v := os.Getenv("LOGKV_SEED"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("bad LOGKV_SEED: %v", err)
		}
		seeds = []int64{n}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runModelSequence(t, seed)
		})
	}
}

func runModelSequence(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	s := newTestStore(t, 1<<20)
	defer s.Close()
	model := newNaiveModel()
	keys := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta"}
	pick := func() string { return keys[rng.Intn(len(keys))] }

	checkGet := func(step int, key string) {
		wantVal, wantSt, basis := model.get(key)
		val, st, err := s.Get([]byte(key))
		if err != nil {
			t.Fatalf("step %d: get %q: %v", step, key, err)
		}
		if st != wantSt || (st == StatusFound && string(val) != wantVal) {
			t.Fatalf("step %d: get %q = (%q,%v), want (%q,%v); %s",
				step, key, val, st, wantVal, wantSt, basis)
		}
		t.Logf("step %03d GET  %-6s -> (%q,%s) | %s", step, key, val, st, basis)
	}

	for step := 0; step < 400; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // put
			key, val := pick(), fmt.Sprintf("v%d", step)
			if err := s.Put([]byte(key), []byte(val)); err != nil {
				t.Fatalf("step %d: put: %v", step, err)
			}
			model.put(key, val)
			t.Logf("step %03d PUT  %-6s <- %q", step, key, val)
		case 4: // delete
			key := pick()
			if err := s.Delete([]byte(key)); err != nil {
				t.Fatalf("step %d: delete: %v", step, err)
			}
			model.del(key)
			t.Logf("step %03d DEL  %-6s", step, key)
		case 5, 6: // get
			checkGet(step, pick())
		case 7: // rotate
			s.mu.Lock()
			if s.active.size > 0 {
				if err := s.rotate(); err != nil {
					s.mu.Unlock()
					t.Fatalf("step %d: rotate: %v", step, err)
				}
				model.rotate()
				t.Logf("step %03d ROTATE -> active seg %d", step, model.active)
			} else {
				t.Logf("step %03d ROTATE skipped (empty active)", step)
			}
			s.mu.Unlock()
		case 8: // merge random sealed subset
			sealed, _ := segIDs(t, s)
			if len(sealed) == 0 {
				t.Logf("step %03d MERGE skipped (no sealed segment)", step)
				continue
			}
			sort.Slice(sealed, func(i, j int) bool { return sealed[i] < sealed[j] })
			var picked []uint32
			for _, id := range sealed {
				if rng.Intn(2) == 0 {
					picked = append(picked, id)
				}
			}
			if len(picked) == 0 {
				picked = []uint32{sealed[rng.Intn(len(sealed))]}
			}
			if err := s.Merge(picked); err != nil {
				t.Fatalf("step %d: merge %v: %v", step, picked, err)
			}
			model.merge(picked)
			t.Logf("step %03d MERGE %v", step, picked)
		case 9: // reopen
			s = reopen(t, s)
			defer s.Close()
			t.Logf("step %03d REOPEN", step)
		}
	}
	// 收尾：全量键对照。
	for _, k := range keys {
		checkGet(999, k)
	}
}
