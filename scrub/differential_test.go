package scrub_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/scrub"
	"ontology/scrub/naive"
)

// store 抽象两个实现共有的操作面，便于差分驱动。
type store interface {
	CreateBlock(id uint64, nodes []uint64, version uint64, digest string, minInterval int64, now int64) error
	Write(id uint64, version uint64, digest string, nodes []uint64, now int64) error
	InjectBitrot(id, nodeID uint64, actualDigest string, now int64) error
	DropReplica(id, nodeID uint64, now int64) error
	Scrub(id uint64, now int64, failNodes map[uint64]bool) (scrub.ScrubResult, error)
	SelectDue(now int64, limit int) ([]uint64, error)
	Alerts() []scrub.Alert
	Inspect(id uint64) (scrub.BlockInfo, error)
}

func errCat(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, scrub.ErrInvalidParam):
		return "InvalidParam"
	case errors.Is(err, scrub.ErrClockRegression):
		return "ClockRegression"
	case errors.Is(err, scrub.ErrBlockNotFound):
		return "BlockNotFound"
	case errors.Is(err, scrub.ErrTooFrequent):
		return "TooFrequent"
	}
	return "other:" + err.Error()
}

const (
	maxBlockID = 6
	maxNodeID  = 8
)

var digests = []string{"a", "b", "c", "d"}

// TestDifferentialRandomOps 用随机操作序列对照真实实现与独立朴素模型，
// 并打印每条操作的输入、输出与判定依据。
func TestDifferentialRandomOps(t *testing.T) {
	outcomeHits := map[scrub.Outcome]int{}
	errHits := map[string]int{}
	for seed := int64(0); seed < 10; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			real := store(scrub.NewService())
			model := naive.New()
			simTime := int64(0)

			for step := 0; step < 400; step++ {
				simTime += int64(rng.Intn(6))
				now := simTime
				clockBack := rng.Intn(10) == 0 && simTime >= 3
				if clockBack {
					now = simTime - int64(1+rng.Intn(3)) // 注入时钟回退
				}

				kind := rng.Intn(100)
				var in, out, basis string

				switch {
				case kind < 15: // 创建块
					id := uint64(rng.Intn(maxBlockID))
					n := 2 + rng.Intn(4)
					perm := rng.Perm(maxNodeID)
					nodes := make([]uint64, n)
					for i := range nodes {
						nodes[i] = uint64(perm[i])
					}
					if rng.Intn(20) == 0 && n > 2 {
						nodes[n-1] = nodes[0] // 注入重复节点（参数非法）
					}
					version := uint64(rng.Intn(4)) // 可能为 0（参数非法）
					digest := digests[rng.Intn(len(digests))]
					if rng.Intn(30) == 0 {
						digest = "" // 注入空摘要（参数非法）
					}
					interval := int64(rng.Intn(8))
					in = fmt.Sprintf("id=%d nodes=%v version=%d digest=%q interval=%d now=%d",
						id, nodes, version, digest, interval, now)
					errR := real.CreateBlock(id, nodes, version, digest, interval, now)
					errM := model.CreateBlock(id, nodes, version, digest, interval, now)
					out = compareErr(t, errR, errM)
					basis = "create: 2..5 副本/节点互异/版本>0/摘要非空 -> 时钟 -> 已存在"

				case kind < 40: // 写入新版本
					id := uint64(rng.Intn(maxBlockID))
					version := uint64(1 + rng.Intn(9))
					digest := digests[rng.Intn(len(digests))]
					nodes := randNodes(rng, 1+rng.Intn(3))
					in = fmt.Sprintf("id=%d version=%d digest=%q nodes=%v now=%d", id, version, digest, nodes, now)
					errR := real.Write(id, version, digest, nodes, now)
					errM := model.Write(id, version, digest, nodes, now)
					out = compareErr(t, errR, errM)
					basis = "write: 参数 -> 时钟 -> 块存在 -> 节点持有副本"

				case kind < 55: // 注入位腐
					id := uint64(rng.Intn(maxBlockID))
					node := uint64(rng.Intn(maxNodeID))
					actual := digests[rng.Intn(len(digests))]
					in = fmt.Sprintf("id=%d node=%d actual=%q now=%d", id, node, actual, now)
					errR := real.InjectBitrot(id, node, actual, now)
					errM := model.InjectBitrot(id, node, actual, now)
					out = compareErr(t, errR, errM)
					basis = "bitrot: 参数 -> 时钟 -> 块存在 -> 节点持有副本"

				case kind < 65: // 丢弃副本
					id := uint64(rng.Intn(maxBlockID))
					node := uint64(rng.Intn(maxNodeID))
					in = fmt.Sprintf("id=%d node=%d now=%d", id, node, now)
					errR := real.DropReplica(id, node, now)
					errM := model.DropReplica(id, node, now)
					out = compareErr(t, errR, errM)
					basis = "drop: 时钟 -> 块存在 -> 节点持有副本；降到 0 块消失"

				case kind < 90: // 巡检
					id := uint64(rng.Intn(maxBlockID))
					fails := map[uint64]bool{}
					for n := 0; n < maxNodeID; n++ {
						if rng.Intn(3) == 0 {
							fails[uint64(n)] = true
						}
					}
					in = fmt.Sprintf("id=%d now=%d failNodes=%v", id, now, sortedKeys(fails))
					resR, errR := real.Scrub(id, now, fails)
					resM, errM := model.Scrub(id, now, fails)
					out = compareErr(t, errR, errM)
					errHits[out]++
					if errR == nil {
						if !reflect.DeepEqual(resR, resM) {
							t.Fatalf("step %d scrub result mismatch:\n real=%+v\nmodel=%+v", step, resR, resM)
						}
						out = fmt.Sprintf("outcome=%s", resR.Outcome)
						outcomeHits[resR.Outcome]++
						basis = fmt.Sprintf("committed=%d auth=(%d,%q) repaired=%v failed=%v",
							resR.CommittedVersion, resR.AuthVersion, resR.AuthDigest, resR.Repaired, resR.Failed)
					} else {
						basis = "scrub: 参数 -> 时钟 -> 块存在 -> 频率"
					}

				default: // 到期选取
					limit := rng.Intn(5) // 可能为 0（参数非法）
					in = fmt.Sprintf("now=%d limit=%d", now, limit)
					idsR, errR := real.SelectDue(now, limit)
					idsM, errM := model.SelectDue(now, limit)
					out = compareErr(t, errR, errM)
					if errR == nil {
						if !reflect.DeepEqual(idsR, idsM) {
							t.Fatalf("step %d select mismatch: real=%v model=%v", step, idsR, idsM)
						}
						out = fmt.Sprintf("due=%v", idsR)
						basis = "select: 未巡检优先 -> lastScrub 早优先 -> 块号小优先"
					} else {
						basis = "select: 参数 -> 时钟"
					}
				}

				t.Logf("step=%03d in=[%s] out=[%s] basis=[%s]", step, in, out, basis)

				// 每条操作后对照完整可观察状态：告警序列与每个块的快照。
				if aR, aM := real.Alerts(), model.Alerts(); !reflect.DeepEqual(aR, aM) {
					t.Fatalf("step %d alerts mismatch:\n real=%+v\nmodel=%+v", step, aR, aM)
				}
				for id := uint64(0); id < maxBlockID; id++ {
					infoR, errR := real.Inspect(id)
					infoM, errM := model.Inspect(id)
					if cR, cM := errCat(errR), errCat(errM); cR != cM {
						t.Fatalf("step %d inspect(%d) err: real=%s model=%s", step, id, cR, cM)
					}
					if errR == nil && !reflect.DeepEqual(infoR, infoM) {
						t.Fatalf("step %d inspect(%d) mismatch:\n real=%+v\nmodel=%+v", step, id, infoR, infoM)
					}
				}
			}
		})
	}
	t.Logf("outcome coverage: %v", outcomeHits)
	t.Logf("scrub error coverage: %v", errHits)
	for o := scrub.OutcomeConsistent; o <= scrub.OutcomeVersionConflict; o++ {
		if outcomeHits[o] == 0 {
			t.Errorf("outcome %s never exercised by random sequence", o)
		}
	}
}

func compareErr(t *testing.T, errR, errM error) string {
	t.Helper()
	cR, cM := errCat(errR), errCat(errM)
	if cR != cM {
		t.Fatalf("error category mismatch: real=%s model=%s", cR, cM)
	}
	return cR
}

func randNodes(rng *rand.Rand, k int) []uint64 {
	perm := rng.Perm(maxNodeID)
	nodes := make([]uint64, k)
	for i := range nodes {
		nodes[i] = uint64(perm[i])
	}
	return nodes
}

func sortedKeys(m map[uint64]bool) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
