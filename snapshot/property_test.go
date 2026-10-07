package snapshot

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 随机生成一条写入。事务号、对象号取自小规模池以制造交错与覆盖；
// 链接有一定概率引用不存在的对象（ghost）以制造引用完整性冲突。
func randomSpec(r *rand.Rand) WriteSpec {
	txn := TxnID(fmt.Sprintf("t%d", r.Intn(4)))
	switch r.Intn(10) {
	case 0, 1, 2: // 链接
		obj := func() string {
			if r.Intn(8) == 0 {
				return "ghost"
			}
			return fmt.Sprintf("o%d", r.Intn(6))
		}
		return linkSpec(txn, fmt.Sprintf("l%d", r.Intn(4)), obj(), obj())
	case 3, 4: // 动作执行记录
		return actSpec(txn, fmt.Sprintf("a%d", r.Intn(4)))
	default: // 对象
		return objSpec(txn, fmt.Sprintf("o%d", r.Intn(6)))
	}
}

type runLog struct {
	Seed     int64    `json:"seed"`
	Writes   []Write  `json:"writes"`
	Boundary LSN      `json:"boundary"`
	Snapshot State    `json:"snapshot,omitempty"`
	NumIncr  int      `json:"increments"`
	Err      string   `json:"error,omitempty"`
	Rules    []string `json:"rules"` // 本次判定所依据的规则
}

// 在大量随机写入序列上，将真实实现与朴素参照模型逐步对照，
// 每次对照记录输入、实际输出与判定所依据的规则。
func TestPropertyAgainstNaiveModel(t *testing.T) {
	const runs = 500
	for seed := int64(0); seed < runs; seed++ {
		r := rand.New(rand.NewSource(seed))
		j := NewJournal()
		n := r.Intn(30)
		for i := 0; i < n; i++ {
			j.AppendRaw(randomSpec(r))
		}
		tip := j.Tip()
		writes := j.Entries(0, tip)

		// 随机选择边界请求：自动，或显式（可能越界/切分事务）。
		var req BoundaryRequest
		if r.Intn(2) == 0 || tip == 0 {
			req = Auto()
		} else {
			req = At(LSN(r.Intn(int(tip) + 3)))
		}

		log := runLog{Seed: seed, Writes: writes, Rules: []string{}}

		sess, err := NewCoordinator(j).BeginExport(req, Options{})
		if err != nil {
			// 边界冲突：朴素模型独立确认该位置确实不合法。
			ee := exportErr(t, err)
			if ee.Kind != ErrBoundaryConflict {
				t.Fatalf("seed %d: expected boundary conflict, got %v", seed, ee)
			}
			at := *req.At
			if at > tip {
				log.Rules = append(log.Rules, RuleBoundaryExists)
			} else if naiveTxnSafe(writes, at) {
				t.Fatalf("seed %d: boundary %d rejected but naive model says txn-safe", seed, at)
			} else {
				log.Rules = append(log.Rules, RuleBoundaryTxnSafe)
			}
			log.Boundary = at
			log.Err = ee.Error()
			dumpRun(t, log)
			continue
		}

		boundary := sess.Boundary()
		log.Boundary = boundary
		log.Rules = append(log.Rules, RuleBoundaryInclusive)
		if req.At == nil && boundary != naiveBoundary(writes) {
			t.Fatalf("seed %d: auto boundary %d != naive %d", seed, boundary, naiveBoundary(writes))
		}

		state, snapErr := sess.Snapshot()
		incrs, drainErr := sess.Drain()

		nState, nIncrs, nErr := naiveExport(writes, boundary)

		// 错误对照：类别与规则必须一致。
		var gotErr *ExportError
		if snapErr != nil {
			gotErr = exportErr(t, snapErr)
		} else if drainErr != nil {
			gotErr = exportErr(t, drainErr)
		}
		if (gotErr == nil) != (nErr == nil) {
			t.Fatalf("seed %d: error mismatch: got %v, naive %v", seed, gotErr, nErr)
		}
		if gotErr != nil {
			if gotErr.Kind != nErr.Kind || gotErr.Rule != nErr.Rule {
				t.Fatalf("seed %d: got %v, naive %v", seed, gotErr, nErr)
			}
			log.Err = gotErr.Error()
			log.Rules = append(log.Rules, gotErr.Rule)
		}
		// 无快照错误时，快照必须与朴素模型一致。
		if snapErr == nil && !reflect.DeepEqual(state, nState) {
			t.Fatalf("seed %d: snapshot mismatch\ngot:  %+v\nnaive: %+v", seed, state, nState)
		}
		if !reflect.DeepEqual(incrs, nIncrs) {
			t.Fatalf("seed %d: increments mismatch\ngot:  %+v\nnaive: %+v", seed, incrs, nIncrs)
		}
		log.Snapshot = state
		log.NumIncr = len(incrs)
		dumpRun(t, log)
	}
}

// dumpRun 以 JSON 形式记录一次对照的输入、输出与判定依据，
// 供复核（go test -v 时可见）。
func dumpRun(t *testing.T, log runLog) {
	t.Helper()
	b, err := json.Marshal(log)
	if err != nil {
		t.Fatalf("marshal run log: %v", err)
	}
	t.Logf("run %s", b)
}
