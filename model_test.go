package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

type opKind int

const (
	opCreate opKind = iota
	opLink
	opUnlink
	opOpen
	opClose
	opShrink
	opBeginShrink
	opFinishShrink
	opCrash
)

type randomOp struct {
	kind   opKind
	arg    int
	target int
}

func (op randomOp) String() string {
	names := [...]string{"Create", "Link", "Unlink", "Open", "Close", "Shrink", "BeginShrink", "FinishShrink", "Crash"}
	switch op.kind {
	case opShrink, opBeginShrink:
		return fmt.Sprintf("%s(%d,%d)", names[op.kind], op.arg, op.target)
	default:
		return fmt.Sprintf("%s(%d)", names[op.kind], op.arg)
	}
}

type modelInode struct {
	links   int
	opens   int
	blocks  int
	pending *int
}

type naiveModel struct {
	P, K, L int
	used    int
	nextID  int
	nextH   int
	inodes  map[int]*modelInode
	handles map[int]int
	orphans []int
	members map[int]bool
}

type opResult struct {
	value int
	err   error
}

type crashCheck struct {
	id     int
	action CrashAction
}

func newNaiveModel(p, k, l int) *naiveModel {
	return &naiveModel{
		P:       p,
		K:       k,
		L:       l,
		nextID:  1,
		nextH:   1,
		inodes:  make(map[int]*modelInode),
		handles: make(map[int]int),
		members: make(map[int]bool),
	}
}

func (m *naiveModel) run(op randomOp) (opResult, []crashCheck) {
	switch op.kind {
	case opCreate:
		if op.arg < 0 {
			return opResult{err: ErrInvalidArgument}, nil
		}
		if m.used+op.arg > m.P {
			return opResult{err: ErrNoSpace}, nil
		}
		id := m.nextID
		m.nextID++
		m.inodes[id] = &modelInode{links: 1, blocks: op.arg}
		m.used += op.arg
		return opResult{value: id}, nil

	case opLink:
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if node.links == 0 {
			return opResult{err: ErrNoLinks}, nil
		}
		if node.links >= m.L {
			return opResult{err: ErrLinksFull}, nil
		}
		node.links++
		return opResult{}, nil

	case opUnlink:
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if node.links == 0 {
			return opResult{err: ErrNoLinks}, nil
		}
		if node.links == 1 && node.opens > 0 && !m.members[op.arg] && len(m.orphans) >= m.K {
			return opResult{err: ErrOrphanListFull}, nil
		}
		node.links--
		if node.links == 0 && node.opens == 0 {
			m.delete(op.arg)
			return opResult{}, nil
		}
		m.sync(op.arg)
		return opResult{}, nil

	case opOpen:
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if node.links == 0 {
			return opResult{err: ErrNoLinks}, nil
		}
		handle := m.nextH
		m.nextH++
		m.handles[handle] = op.arg
		node.opens++
		return opResult{value: handle}, nil

	case opClose:
		id, ok := m.handles[op.arg]
		if !ok {
			return opResult{err: ErrNotFound}, nil
		}
		node := m.inodes[id]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		delete(m.handles, op.arg)
		node.opens--
		if node.links == 0 && node.opens == 0 {
			m.delete(id)
			return opResult{}, nil
		}
		m.sync(id)
		return opResult{}, nil

	case opShrink:
		if op.target < 0 {
			return opResult{err: ErrInvalidArgument}, nil
		}
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if op.target > node.blocks {
			return opResult{err: ErrInvalidArgument}, nil
		}
		if node.pending != nil {
			return opResult{err: ErrShrinkInProgress}, nil
		}
		m.used -= node.blocks - op.target
		node.blocks = op.target
		return opResult{}, nil

	case opBeginShrink:
		if op.target < 0 {
			return opResult{err: ErrInvalidArgument}, nil
		}
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if op.target > node.blocks {
			return opResult{err: ErrInvalidArgument}, nil
		}
		if node.pending != nil {
			return opResult{err: ErrShrinkInProgress}, nil
		}
		if !m.members[op.arg] && len(m.orphans) >= m.K {
			return opResult{err: ErrOrphanListFull}, nil
		}
		target := op.target
		node.pending = &target
		m.sync(op.arg)
		return opResult{}, nil

	case opFinishShrink:
		node := m.inodes[op.arg]
		if node == nil {
			return opResult{err: ErrNotFound}, nil
		}
		if node.pending == nil {
			return opResult{err: ErrNoPendingShrink}, nil
		}
		target := *node.pending
		node.pending = nil
		m.used -= node.blocks - target
		node.blocks = target
		m.sync(op.arg)
		return opResult{}, nil

	case opCrash:
		ids := append([]int(nil), m.orphans...)
		checks := make([]crashCheck, 0, len(ids))
		for _, id := range ids {
			node := m.inodes[id]
			if node == nil {
				continue
			}
			if node.links == 0 {
				checks = append(checks, crashCheck{id, CrashDelete})
				m.delete(id)
				continue
			}
			target := 0
			if node.pending != nil {
				target = *node.pending
			}
			m.used -= node.blocks - target
			node.blocks = target
			node.pending = nil
			node.opens = 0
			checks = append(checks, crashCheck{id, CrashTruncate})
		}
		for _, node := range m.inodes {
			node.opens = 0
		}
		m.handles = make(map[int]int)
		m.nextH = 1
		m.orphans = nil
		m.members = make(map[int]bool)
		return opResult{}, checks
	}

	return opResult{}, nil
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		rng := rand.New(rand.NewSource(int64(100000 + trial)))
		p := rng.Intn(40) + 1
		k := rng.Intn(4) + 1
		l := rng.Intn(5) + 1
		ledger := newTestLedger(t, p, k, l)
		model := newNaiveModel(p, k, l)

		var log strings.Builder
		fmt.Fprintf(&log, "trial=%d P=%d K=%d L=%d\n", trial, p, k, l)

		for step := 0; step < rng.Intn(120)+1; step++ {
			op := generateRandomOp(rng, model)
			fmt.Fprintf(&log, "step=%d input=%s\n", step, op)

			modelResult, modelCrash := model.run(op)
			actualResult, actualCrash := runActual(t, ledger, op)

			fmt.Fprintf(&log, "model={value:%d err:%v crash:%v} actual={value:%d err:%v crash:%v}\n",
				modelResult.value, modelResult.err, modelCrash,
				actualResult.value, actualResult.err, actualCrash)

			if !sameErrors(modelResult.err, actualResult.err) ||
				modelResult.value != actualResult.value ||
				!reflect.DeepEqual(modelCrash, actualCrash) {
				t.Fatalf("operation mismatch\n%s", log.String())
			}
			if err := compareSnapshots(t, ledger, model, &log); err != nil {
				t.Fatalf("state mismatch: %v\n%s", err, log.String())
			}
		}
	}
}

func generateRandomOp(rng *rand.Rand, model *naiveModel) randomOp {
	if len(model.inodes) == 0 || rng.Intn(6) == 0 {
		return randomOp{kind: opCreate, arg: rng.Intn(model.P + 3)}
	}

	ids := make([]int, 0, len(model.inodes))
	for id := range model.inodes {
		ids = append(ids, id)
	}
	id := ids[rng.Intn(len(ids))]
	node := model.inodes[id]

	switch rng.Intn(12) {
	case 0:
		return randomOp{kind: opLink, arg: id}
	case 1:
		return randomOp{kind: opUnlink, arg: id}
	case 2:
		return randomOp{kind: opOpen, arg: id}
	case 3:
		if rng.Intn(4) == 0 || len(model.handles) == 0 {
			return randomOp{kind: opClose, arg: rng.Intn(model.nextH + 2)}
		}
		handles := make([]int, 0, len(model.handles))
		for handle := range model.handles {
			handles = append(handles, handle)
		}
		return randomOp{kind: opClose, arg: handles[rng.Intn(len(handles))]}
	case 4, 5:
		return randomOp{kind: opShrink, arg: id, target: rng.Intn(node.blocks+2) - 1}
	case 6, 7:
		return randomOp{kind: opBeginShrink, arg: id, target: rng.Intn(node.blocks+2) - 1}
	case 8:
		return randomOp{kind: opFinishShrink, arg: id}
	case 9:
		return randomOp{kind: opCrash}
	case 10:
		return randomOp{kind: opOpen, arg: id}
	default:
		return randomOp{kind: opUnlink, arg: id}
	}
}

func (m *naiveModel) sync(id int) {
	node := m.inodes[id]
	isMember := node != nil && (node.pending != nil || node.links == 0 && node.opens > 0)
	if isMember {
		if !m.members[id] {
			m.orphans = append([]int{id}, m.orphans...)
			m.members[id] = true
		}
		return
	}
	m.remove(id)
}

func (m *naiveModel) remove(id int) {
	delete(m.members, id)
	for i, current := range m.orphans {
		if current == id {
			m.orphans = append(m.orphans[:i], m.orphans[i+1:]...)
			return
		}
	}
}

func (m *naiveModel) delete(id int) {
	node := m.inodes[id]
	if node == nil {
		return
	}
	m.used -= node.blocks
	delete(m.inodes, id)
	m.remove(id)
}

func runActual(t *testing.T, ledger *Ledger, op randomOp) (opResult, []crashCheck) {
	t.Helper()
	switch op.kind {
	case opCreate:
		value, err := ledger.Create(op.arg)
		return opResult{value, err}, nil
	case opLink:
		return opResult{err: ledger.Link(op.arg)}, nil
	case opUnlink:
		return opResult{err: ledger.Unlink(op.arg)}, nil
	case opOpen:
		value, err := ledger.Open(op.arg)
		return opResult{value, err}, nil
	case opClose:
		return opResult{err: ledger.Close(op.arg)}, nil
	case opShrink:
		return opResult{err: ledger.Shrink(op.arg, op.target)}, nil
	case opBeginShrink:
		return opResult{err: ledger.BeginShrink(op.arg, op.target)}, nil
	case opFinishShrink:
		return opResult{err: ledger.FinishShrink(op.arg)}, nil
	case opCrash:
		results := ledger.Crash()
		checks := make([]crashCheck, len(results))
		for i, result := range results {
			checks[i] = crashCheck{result.Inode, result.Action}
		}
		return opResult{}, checks
	}
	return opResult{}, nil
}

func sameErrors(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

func compareSnapshots(t *testing.T, ledger *Ledger, model *naiveModel, log *strings.Builder) error {
	t.Helper()
	if got := ledger.Used(); got != model.used {
		return fmt.Errorf("Used actual=%d model=%d", got, model.used)
	}
	actualOrphans := ledger.OrphanIDs()
	wantOrphans := model.orphans
	if len(actualOrphans) == 0 {
		actualOrphans = nil
	}
	if len(wantOrphans) == 0 {
		wantOrphans = nil
	}
	if !reflect.DeepEqual(actualOrphans, wantOrphans) {
		return fmt.Errorf("orphans actual=%v model=%v", actualOrphans, wantOrphans)
	}
	for id, node := range model.inodes {
		links, err := ledger.Links(id)
		if err != nil || links != node.links {
			return fmt.Errorf("inode %d links actual=(%d,%v) model=%d", id, links, err, node.links)
		}
		opens, err := ledger.OpenCount(id)
		if err != nil || opens != node.opens {
			return fmt.Errorf("inode %d opens actual=(%d,%v) model=%d", id, opens, err, node.opens)
		}
		blocks, err := ledger.Blocks(id)
		if err != nil || blocks != node.blocks {
			return fmt.Errorf("inode %d blocks actual=(%d,%v) model=%d", id, blocks, err, node.blocks)
		}
		gotPending, gotHas, err := ledger.PendingShrink(id)
		wantPending, wantHas := 0, node.pending != nil
		if node.pending != nil {
			wantPending = *node.pending
		}
		if err != nil || gotHas != wantHas || gotPending != wantPending {
			return fmt.Errorf("inode %d pending actual=(%d,%t,%v) model=(%d,%t)", id, gotPending, gotHas, err, wantPending, wantHas)
		}
	}
	if len(model.inodes) > 0 {
		highest := 0
		for id := range model.inodes {
			if id > highest {
				highest = id
			}
		}
		for id := 1; id <= highest; id++ {
			exists := ledger.Exists(id)
			_, inModel := model.inodes[id]
			if exists != inModel {
				return fmt.Errorf("inode %d existence actual=%t model=%t", id, exists, inModel)
			}
		}
	}
	return nil
}
