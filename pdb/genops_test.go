package pdb

import "math/rand"

// opKind enumerates operation types used by the differential fuzzer.
type opKind int

const (
	opUpsertPod opKind = iota
	opDeletePod
	opUpsertBudget
	opDeleteBudget
	opEvict
	opBatch
	opConfirm
	opCancel
	opQuery
	opCount
)

type diffOp struct {
	kind  opKind
	pod   Pod
	ref   PodRef
	nn    NamespacedName
	bud   BudgetSpec
	batch []PodRef
	now   int64
}

func (o diffOp) name() string {
	switch o.kind {
	case opUpsertPod:
		return "UpsertPod"
	case opDeletePod:
		return "DeletePod"
	case opUpsertBudget:
		return "UpsertBudget"
	case opDeleteBudget:
		return "DeleteBudget"
	case opEvict:
		return "Evict"
	case opBatch:
		return "EvictBatch"
	case opConfirm:
		return "Confirm"
	case opCancel:
		return "Cancel"
	default:
		return "Budget"
	}
}

func opInput(op diffOp) string {
	switch op.kind {
	case opEvict, opDeletePod, opConfirm, opCancel:
		return op.ref.Namespace + "/" + op.ref.UID
	case opUpsertPod:
		return op.pod.Namespace + "/" + op.pod.UID
	case opUpsertBudget, opDeleteBudget, opQuery:
		return op.nn.Namespace + "/" + op.nn.Name
	case opBatch:
		s := ""
		for i, r := range op.batch {
			if i > 0 {
				s += ","
			}
			s += r.UID
		}
		return "[" + s + "]"
	}
	return ""
}

func podLetter(i int) string { return string(rune('a' + i)) }

func randomLabels(rng *rand.Rand, keys []string, vals map[string][]string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		if rng.Intn(2) == 1 {
			out[k] = vals[k][rng.Intn(len(vals[k]))]
		}
	}
	return out
}

func randomSelector(rng *rand.Rand, keys []string, vals map[string][]string) Selector {
	sel := Selector{}
	for _, k := range keys {
		if rng.Intn(2) == 1 {
			sel[k] = vals[k][rng.Intn(len(vals[k]))]
		}
	}
	return sel
}

// generateOps builds a randomized operation stream over one namespace. The
// generated streams deliberately include illegal refs, invalid budgets and
// clock rollbacks so every error branch is exercised.
func generateOps(rng *rand.Rand, steps int) []diffOp {
	const ns = "ns"
	podCount := 6
	budgetNames := []string{"A", "B", "C"}
	labelKeys := []string{"app", "tier", "zone"}
	labelVals := map[string][]string{
		"app":  {"x", "y"},
		"tier": {"fe", "be"},
		"zone": {"z1", "z2"},
	}
	pickRef := func() PodRef {
		return PodRef{Namespace: ns, UID: podLetter(rng.Intn(podCount))}
	}

	ops := make([]diffOp, 0, steps)
	t := int64(0)
	for i := 0; i < steps; i++ {
		// Advance time; occasional big jumps trigger grace expiry.
		switch rng.Intn(10) {
		case 0:
			t += int64(rng.Intn(25) + 20)
		default:
			t++
		}
		op := diffOp{now: t, kind: opKind(rng.Intn(int(opCount)))}
		switch op.kind {
		case opUpsertPod:
			op.pod = Pod{
				UID:       podLetter(rng.Intn(podCount)),
				Namespace: ns,
				Phase:     []Phase{PhaseRunning, PhaseRunning, PhaseRunning, PhasePending, PhaseSucceeded, PhaseFailed}[rng.Intn(6)],
				Ready:     rng.Intn(2) == 1,
				Labels:    randomLabels(rng, labelKeys, labelVals),
			}
			op.ref = PodRef{ns, op.pod.UID}
			// Rarely emit a structurally invalid phase or empty uid.
			if rng.Intn(25) == 0 {
				op.pod.UID = ""
			}
		case opDeletePod:
			op.ref = pickRef()
		case opUpsertBudget:
			name := budgetNames[rng.Intn(len(budgetNames))]
			op.bud = BudgetSpec{
				Name:      name,
				Namespace: ns,
				Selector:  randomSelector(rng, labelKeys, labelVals),
			}
			r := rng.Intn(4)
			switch r {
			case 0:
				op.bud.MinAvailable = &Value{Amount: rng.Intn(5)}
			case 1:
				op.bud.MinAvailable = &Value{IsPercent: true, Amount: rng.Intn(101)}
			case 2:
				op.bud.MaxUnavailable = &Value{Amount: rng.Intn(5)}
			default:
				op.bud.MaxUnavailable = &Value{IsPercent: true, Amount: rng.Intn(101)}
			}
			// Occasionally produce both-set (invalid) or empty values.
			if rng.Intn(20) == 0 {
				op.bud.MinAvailable = &Value{Amount: 1}
				op.bud.MaxUnavailable = &Value{Amount: 1}
			}
			op.nn = NamespacedName{ns, name}
		case opDeleteBudget:
			op.nn = NamespacedName{ns, budgetNames[rng.Intn(len(budgetNames))]}
		case opEvict:
			op.ref = pickRef()
			if rng.Intn(20) == 0 {
				op.ref.UID = ""
			}
		case opBatch:
			n := 1 + rng.Intn(4)
			for j := 0; j < n; j++ {
				op.batch = append(op.batch, PodRef{ns, podLetter(rng.Intn(podCount))})
			}
			if rng.Intn(15) == 0 && len(op.batch) >= 2 {
				op.batch[1] = op.batch[0] // duplicate
			}
		case opConfirm, opCancel:
			op.ref = pickRef()
		case opQuery:
			op.nn = NamespacedName{ns, budgetNames[rng.Intn(len(budgetNames))]}
		}
		ops = append(ops, op)
	}
	// Inject explicit rollback timestamps every now and then.
	for i := 1; i < len(ops); i++ {
		if rng.Intn(15) == 0 {
			ops[i].now = ops[i-1].now - 1
		}
	}
	return ops
}
