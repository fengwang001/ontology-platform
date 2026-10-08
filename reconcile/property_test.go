package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// 朴素参照模型。
//
// naiveReconcile 是对规则的逐步独立实现：不做任何性能优化，
// 用最直接的嵌套循环表达题面规则，作为属性测试的对照基准。
// 它与正式实现共享信封解析（解析逻辑不属于被对照的裁决逻辑），
// 但基准确定、基准过滤、冲突裁决、不可和解判定均独立实现。

type naiveOutcome struct {
	Baseline     uint64
	State        map[string]map[string]string
	Issues       []ObjectIssue
	Faults       []ReplicaFault
	Participants []string
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func naiveReconcile(blobs [][]byte) (*naiveOutcome, error) {
	type readable struct {
		snap Snapshot
	}
	var parts []readable
	var faults []ReplicaFault
	hints := map[string]bool{}

	for _, blob := range blobs {
		d := decodeEnvelope(blob)
		switch {
		case !d.headerOK:
			faults = append(faults, ReplicaFault{
				Kind:   CorruptWhole,
				Detail: "头部不可读或校验失败，整个副本无法解析",
				Digest: digestBytes(blob),
			})
		case len(d.badEntries) > 0:
			faults = append(faults, ReplicaFault{
				ReplicaID: d.snap.ReplicaID,
				Kind:      CorruptPartial,
				Detail:    fmt.Sprintf("条目校验失败: %v", d.badEntries),
				Digest:    digestBytes(blob),
			})
			for _, obj := range d.snap.Objects {
				hints[obj.ObjectID] = true
			}
		default:
			parts = append(parts, readable{snap: d.snap})
		}
	}

	if len(parts) == 0 {
		return nil, ErrInsufficientReplicas
	}

	// 基准：逐个比较取最早位点。
	baseline := parts[0].snap.Position
	for _, p := range parts {
		if p.snap.Position < baseline {
			baseline = p.snap.Position
		}
	}

	// 收集每个 (对象, 属性) 的全部候选（晚于基准的一律跳过）。
	type cand struct {
		priority uint64
		value    string
	}
	cell := map[string]map[string][]cand{}
	for _, p := range parts {
		for _, obj := range p.snap.Objects {
			for name, a := range obj.Attributes {
				if a.WrittenAt > baseline {
					continue
				}
				if cell[obj.ObjectID] == nil {
					cell[obj.ObjectID] = map[string][]cand{}
				}
				cell[obj.ObjectID][name] = append(cell[obj.ObjectID][name], cand{p.snap.Priority, a.Value})
			}
		}
	}

	out := &naiveOutcome{
		Baseline: baseline,
		State:    map[string]map[string]string{},
	}
	for _, p := range parts {
		out.Participants = append(out.Participants, p.snap.ReplicaID)
	}
	sort.Strings(out.Participants)

	// 逐对象逐属性裁决：先找最高优先级，再看该优先级上取值是否唯一。
	for objectID, attrs := range cell {
		for name, cands := range attrs {
			allSame := true
			for _, c := range cands {
				if c.value != cands[0].value {
					allSame = false
				}
			}
			if allSame {
				if out.State[objectID] == nil {
					out.State[objectID] = map[string]string{}
				}
				out.State[objectID][name] = cands[0].value
				continue
			}
			top := cands[0].priority
			for _, c := range cands {
				if c.priority > top {
					top = c.priority
				}
			}
			distinct := map[string]bool{}
			for _, c := range cands {
				if c.priority == top {
					distinct[c.value] = true
				}
			}
			if len(distinct) == 1 {
				for v := range distinct {
					if out.State[objectID] == nil {
						out.State[objectID] = map[string]string{}
					}
					out.State[objectID][name] = v
				}
			} else {
				out.Issues = append(out.Issues, ObjectIssue{
					ObjectID:  objectID,
					Attribute: name,
					Reason:    ReasonConflictTie,
				})
			}
		}
	}

	// 仅存在于被隔离副本中的对象：来源不足。
	for objectID := range hints {
		if _, ok := cell[objectID]; !ok {
			out.Issues = append(out.Issues, ObjectIssue{
				ObjectID: objectID,
				Reason:   ReasonInsufficientParticipants,
			})
		}
	}

	sort.Slice(out.Issues, func(i, j int) bool {
		if out.Issues[i].ObjectID != out.Issues[j].ObjectID {
			return out.Issues[i].ObjectID < out.Issues[j].ObjectID
		}
		return out.Issues[i].Attribute < out.Issues[j].Attribute
	})
	sort.Slice(faults, func(i, j int) bool {
		if faults[i].ReplicaID != faults[j].ReplicaID {
			return faults[i].ReplicaID < faults[j].ReplicaID
		}
		return faults[i].Digest < faults[j].Digest
	})
	out.Faults = faults
	return out, nil
}

// realOutcome 将正式实现的结果投影为可对照结构。
func realOutcome(res *Result) *naiveOutcome {
	var issues []ObjectIssue
	for _, is := range res.Report.Issues {
		// 对照时忽略叙述性字段，只比较判定要素。
		issues = append(issues, ObjectIssue{ObjectID: is.ObjectID, Attribute: is.Attribute, Reason: is.Reason})
	}
	return &naiveOutcome{
		Baseline:     res.Baseline,
		State:        res.State,
		Issues:       issues,
		Faults:       res.Report.Faults,
		Participants: res.Report.Participants,
	}
}

// 随机输入生成。

func randomBlobs(rng *rand.Rand) [][]byte {
	nReplicas := 1 + rng.Intn(8)
	blobs := make([][]byte, 0, nReplicas)
	for r := 0; r < nReplicas; r++ {
		s := Snapshot{
			ReplicaID: fmt.Sprintf("replica-%d", r),
			Priority:  uint64(rng.Intn(4)), // 小范围优先级，制造并列
			Position:  uint64(1 + rng.Intn(5)),
		}
		for o := 0; o < 12; o++ {
			if rng.Float64() >= 0.6 {
				continue
			}
			entry := ObjectEntry{
				ObjectID:   fmt.Sprintf("obj-%d", o),
				Attributes: map[string]AttributeValue{},
			}
			for a := 0; a < 3; a++ {
				if rng.Float64() >= 0.7 {
					continue
				}
				entry.Attributes[fmt.Sprintf("attr-%d", a)] = AttributeValue{
					Value:     fmt.Sprintf("v%d", rng.Intn(5)),
					WrittenAt: uint64(rng.Intn(8)),
				}
			}
			if len(entry.Attributes) > 0 {
				s.Objects = append(s.Objects, entry)
			}
		}
		blob := EncodeSnapshot(s)
		switch roll := rng.Float64(); {
		case roll < 0.10 && len(s.Objects) > 0:
			blob = tamperRandomEntry(rng, blob)
		case roll < 0.20:
			blob = tamperHeader(rng, blob)
		}
		blobs = append(blobs, blob)
	}
	return blobs
}

func tamperHeader(rng *rand.Rand, blob []byte) []byte {
	var env envelopeJSON
	if err := json.Unmarshal(blob, &env); err != nil {
		return blob
	}
	env.Header.Position += 1 + uint64(rng.Intn(100))
	out, err := json.Marshal(env)
	if err != nil {
		return blob
	}
	return out
}

func tamperRandomEntry(rng *rand.Rand, blob []byte) []byte {
	var env envelopeJSON
	if err := json.Unmarshal(blob, &env); err != nil {
		return blob
	}
	i := rng.Intn(len(env.Entries))
	for name, a := range env.Entries[i].Attributes {
		a.Value += "-tampered"
		env.Entries[i].Attributes[name] = a
	}
	out, err := json.Marshal(env)
	if err != nil {
		return blob
	}
	return out
}

// 判定依据记录。

type reconcileRecord struct {
	Iteration int            `json:"iteration"`
	Inputs    []Snapshot     `json:"inputs"`
	Corrupted []ReplicaFault `json:"corrupted"`
	Result    *Result        `json:"result,omitempty"`
	Err       string         `json:"error,omitempty"`
}

func decodeAll(blobs [][]byte) []Snapshot {
	out := make([]Snapshot, 0, len(blobs))
	for _, b := range blobs {
		d := decodeEnvelope(b)
		out = append(out, d.snap)
	}
	return out
}

// TestPropertyAgainstNaiveModel 在大量随机构造的多副本快照组合上，
// 将正式实现与朴素参照模型逐步对照；同时验证同一输入乱序后结果不变。
// 设置 RECONCILE_RECORD_DIR 环境变量可将每次和解的输入、输出与
// 判定依据记录为 JSONL 文件。
func TestPropertyAgainstNaiveModel(t *testing.T) {
	const iterations = 1000
	rng := rand.New(rand.NewSource(20261007))

	var recordFile *os.File
	if dir := os.Getenv("RECONCILE_RECORD_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "reconcile-records.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		recordFile = f
	}

	for i := 0; i < iterations; i++ {
		blobs := randomBlobs(rng)

		realRes, realErr := NewReconciler().Reconcile(blobs)
		naiveRes, naiveErr := naiveReconcile(blobs)

		if recordFile != nil {
			rec := reconcileRecord{
				Iteration: i,
				Inputs:    decodeAll(blobs),
				Result:    realRes,
			}
			if realRes != nil {
				rec.Corrupted = realRes.Report.Faults
			}
			if realErr != nil {
				rec.Err = realErr.Error()
			}
			line, _ := json.Marshal(rec)
			if _, err := recordFile.Write(append(line, '\n')); err != nil {
				t.Fatal(err)
			}
		}

		if (realErr == nil) != (naiveErr == nil) {
			t.Fatalf("第 %d 次：错误不一致 real=%v naive=%v", i, realErr, naiveErr)
		}
		if realErr != nil {
			if !errors.Is(realErr, ErrInsufficientReplicas) || !errors.Is(naiveErr, ErrInsufficientReplicas) {
				t.Fatalf("第 %d 次：非预期错误 real=%v naive=%v", i, realErr, naiveErr)
			}
			continue
		}

		realJSON, _ := json.Marshal(realOutcome(realRes))
		naiveJSON, _ := json.Marshal(naiveRes)
		if string(realJSON) != string(naiveJSON) {
			t.Fatalf("第 %d 次：与参照模型不一致\nreal:  %s\nnaive: %s", i, realJSON, naiveJSON)
		}

		// 同一组输入乱序后结果必须完全一致。
		perm := rng.Perm(len(blobs))
		shuffled := make([][]byte, len(blobs))
		for j, p := range perm {
			shuffled[j] = blobs[p]
		}
		res2, err := NewReconciler().Reconcile(shuffled)
		if err != nil {
			t.Fatalf("第 %d 次：乱序后和解失败: %v", i, err)
		}
		got, _ := json.Marshal(realOutcome(res2))
		if string(got) != string(realJSON) {
			t.Fatalf("第 %d 次：乱序后结果漂移", i)
		}
	}
}
