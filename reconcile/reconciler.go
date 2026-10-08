package reconcile

import (
	"fmt"
	"sort"
)

// Stats 记录一次和解的工作量计数，用于复核开销只与
// 真正存在冲突的对象实例相关，而非随副本数×对象数失控增长。
type Stats struct {
	ReplicasReceived    int `json:"replicas_received"`
	ReplicasParticipant int `json:"replicas_participant"`
	ReplicasQuarantined int `json:"replicas_quarantined"`
	// EntriesScanned 是单次索引扫描处理的属性取值总数（线性下界）。
	EntriesScanned int `json:"entries_scanned"`
	// EntriesExcludedByBaseline 是因晚于位点基准而被排除的取值数。
	EntriesExcludedByBaseline int `json:"entries_excluded_by_baseline"`
	// AdjudicationsRun 是实际触发裁决的属性数，等于真实冲突数。
	AdjudicationsRun int `json:"adjudications_run"`
	// PriorityComparisons 是裁决过程中执行的优先级比较总次数。
	PriorityComparisons int `json:"priority_comparisons"`
}

// DecisionKind 标识判定依据记录的类别。
type DecisionKind string

const (
	DecisionBaseline     DecisionKind = "baseline"
	DecisionQuarantine   DecisionKind = "quarantine"
	DecisionAdjudicate   DecisionKind = "adjudicate"
	DecisionInsufficient DecisionKind = "insufficient"
)

// Decision 是一条判定依据记录：每次和解的输入、输出与裁决理由
// 都可以通过日志序列完整回放。
type Decision struct {
	Kind       DecisionKind `json:"kind"`
	ReplicaID  string       `json:"replica_id,omitempty"`
	ObjectID   string       `json:"object_id,omitempty"`
	Attribute  string       `json:"attribute,omitempty"`
	Detail     string       `json:"detail"`
	Candidates []Candidate  `json:"candidates,omitempty"`
	Winner     *Candidate   `json:"winner,omitempty"`
}

// Report 是一次和解的完整报告。
type Report struct {
	Baseline     uint64         `json:"baseline"`
	Participants []string       `json:"participants"`
	Faults       []ReplicaFault `json:"faults,omitempty"`
	Issues       []ObjectIssue  `json:"issues,omitempty"`
	Stats        Stats          `json:"stats"`
	Journal      []Decision     `json:"journal"`
}

// Result 是一次和解的唯一确定输出。
type Result struct {
	// Baseline 是本次和解对应的逻辑位点（可参与副本中最早者）。
	Baseline uint64
	// State 是和解后的状态：对象 ID -> 属性名 -> 唯一保留取值。
	State  map[string]map[string]string
	Report Report
}

// Reconciler 是对账和解组件的编排入口。它不持有任何状态，
// 对同一组输入的并发调用得到完全一致的结果，且不修改输入。
type Reconciler struct{}

// NewReconciler 返回一个可并发安全使用的和解组件。
func NewReconciler() Reconciler { return Reconciler{} }

// Reconcile 将一组副本快照信封归并为一份和解结果。
//
// 流程按错误判定优先级固定为：损坏隔离（确定可读集合），
// 位点基准（不足则整体失败），逐对象冲突裁决（不可和解逐对象记录）。
func (Reconciler) Reconcile(blobs [][]byte) (*Result, error) {
	q := isolate(blobs)

	baseline, err := resolveBaseline(q.participants)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Baseline: baseline,
		State:    map[string]map[string]string{},
	}
	report := &res.Report
	report.Baseline = baseline
	report.Faults = q.faults
	report.Stats.ReplicasReceived = len(blobs)
	report.Stats.ReplicasParticipant = len(q.participants)
	report.Stats.ReplicasQuarantined = len(q.faults)

	for _, p := range q.participants {
		report.Participants = append(report.Participants, p.snap.ReplicaID)
	}
	report.Journal = append(report.Journal, Decision{
		Kind:   DecisionBaseline,
		Detail: fmt.Sprintf("位点基准确定为 %d（%d 个可参与副本中的最早位点）", baseline, len(q.participants)),
	})
	for _, f := range q.faults {
		report.Journal = append(report.Journal, Decision{
			Kind:      DecisionQuarantine,
			ReplicaID: f.ReplicaID,
			Detail:    f.Detail,
		})
	}

	// 单次扫描建立索引：对象 -> 属性 -> 候选取值集合。
	// 晚于基准位点写入的取值一律不计入。
	index := map[string]map[string][]Candidate{}
	for _, p := range q.participants {
		for _, obj := range p.snap.Objects {
			for name, a := range obj.Attributes {
				report.Stats.EntriesScanned++
				if a.WrittenAt > baseline {
					report.Stats.EntriesExcludedByBaseline++
					continue
				}
				attrs, ok := index[obj.ObjectID]
				if !ok {
					attrs = map[string][]Candidate{}
					index[obj.ObjectID] = attrs
				}
				attrs[name] = append(attrs[name], Candidate{
					ReplicaID: p.snap.ReplicaID,
					Priority:  p.snap.Priority,
					Value:     a.Value,
					WrittenAt: a.WrittenAt,
				})
			}
		}
	}

	for _, objectID := range sortedKeys(index) {
		attrs := index[objectID]
		for _, attr := range sortedKeys(attrs) {
			cands := attrs[attr]
			if !hasConflict(cands) {
				putValue(res.State, objectID, attr, cands[0].Value)
				continue
			}

			report.Stats.AdjudicationsRun++
			winner, verdict, comparisons := adjudicate(cands)
			report.Stats.PriorityComparisons += comparisons

			sorted := sortedCandidates(cands)
			d := Decision{
				Kind:       DecisionAdjudicate,
				ObjectID:   objectID,
				Attribute:  attr,
				Candidates: sorted,
			}
			if verdict == VerdictResolved {
				putValue(res.State, objectID, attr, winner.Value)
				w := winner
				d.Winner = &w
				d.Detail = fmt.Sprintf("按优先标识裁决：副本 %s（优先级 %d）的取值唯一保留", winner.ReplicaID, winner.Priority)
			} else {
				d.Detail = "最高优先级上存在并列的不同取值，判定为不可和解"
				report.Issues = append(report.Issues, ObjectIssue{
					ObjectID:  objectID,
					Attribute: attr,
					Reason:    ReasonConflictTie,
					Detail:    d.Detail,
				})
			}
			report.Journal = append(report.Journal, d)
		}
	}

	// 仅存在于被隔离副本中的对象：剩余副本不足以确定其取值。
	for _, objectID := range sortedKeys(q.hints) {
		if _, ok := index[objectID]; ok {
			continue
		}
		replicas := q.hints[objectID]
		report.Issues = append(report.Issues, ObjectIssue{
			ObjectID: objectID,
			Reason:   ReasonInsufficientParticipants,
			Detail:   fmt.Sprintf("对象的所有来源副本均被隔离（%v），无法确定唯一取值", replicas),
		})
		report.Journal = append(report.Journal, Decision{
			Kind:     DecisionInsufficient,
			ObjectID: objectID,
			Detail:   fmt.Sprintf("来源副本 %v 均被隔离，剩余副本不足以确定取值", replicas),
		})
	}

	sort.Slice(report.Issues, func(i, j int) bool {
		a, b := report.Issues[i], report.Issues[j]
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		return a.Attribute < b.Attribute
	})
	return res, nil
}

// hasConflict 报告候选集合中是否存在两个及以上不同取值。
func hasConflict(cands []Candidate) bool {
	for _, c := range cands[1:] {
		if c.Value != cands[0].Value {
			return true
		}
	}
	return false
}

func putValue(state map[string]map[string]string, objectID, attr, value string) {
	attrs, ok := state[objectID]
	if !ok {
		attrs = map[string]string{}
		state[objectID] = attrs
	}
	attrs[attr] = value
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedCandidates 返回候选的确定性排序副本，供日志记录使用；
// 排序只影响展示，不影响裁决结果。
func sortedCandidates(cands []Candidate) []Candidate {
	out := make([]Candidate, len(cands))
	copy(out, cands)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.ReplicaID != b.ReplicaID {
			return a.ReplicaID < b.ReplicaID
		}
		return a.Value < b.Value
	})
	return out
}
