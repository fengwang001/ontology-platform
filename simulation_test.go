package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type simStatus int

const (
	simActive simStatus = iota
	simPrepared
	simCommitted
	simAborted
)

type simTx struct {
	id       int
	readTime int
	endTime  int
	status   simStatus
	finished bool
}

type simVersion struct {
	value   int
	creator int
	ender   int
}

type simModel struct {
	k          int
	clock      int
	nextTx     int
	txs        map[int]*simTx
	versions   map[int][]*simVersion
	deps       map[int]map[int]struct{}
	dependents map[int]map[int]struct{}
}

type simOperation struct {
	name  string
	tx    int
	key   int
	value int
}

type simResult struct {
	intValue int
	ints     []int
	err      error
	reason   string
}

func newSimModel(k int) *simModel {
	m := &simModel{
		k:          k,
		nextTx:     1,
		txs:        make(map[int]*simTx),
		versions:   make(map[int][]*simVersion),
		deps:       make(map[int]map[int]struct{}),
		dependents: make(map[int]map[int]struct{}),
	}
	for key := 0; key < k; key++ {
		m.versions[key] = []*simVersion{{value: 0, creator: noTx, ender: noTx}}
	}
	return m
}

func (m *simModel) execute(op simOperation) simResult {
	switch op.name {
	case "Begin":
		return m.begin()
	case "Read":
		return m.read(op.tx, op.key)
	case "Write":
		return m.write(op.tx, op.key, op.value)
	case "Precommit":
		return m.precommit(op.tx)
	case "Finish":
		return m.finish(op.tx)
	case "Abort":
		return m.abort(op.tx)
	default:
		panic("unknown operation")
	}
}

func (m *simModel) begin() simResult {
	m.clock++
	id := m.nextTx
	m.nextTx++
	m.txs[id] = &simTx{id: id, readTime: m.clock, status: simActive}
	m.deps[id] = make(map[int]struct{})
	m.dependents[id] = make(map[int]struct{})
	return simResult{
		intValue: id,
		reason:   fmt.Sprintf("begin: RT=%d ID=%d", m.clock, id),
	}
}

func (m *simModel) precommit(id int) simResult {
	if _, ok := m.txs[id]; !ok {
		return simResult{err: ErrUnknownTx, reason: "reject: unknown transaction"}
	}
	if m.txs[id].status != simActive {
		return simResult{err: ErrInvalidStatus, reason: "reject: transaction is not active"}
	}
	m.clock++
	tx := m.txs[id]
	tx.status = simPrepared
	tx.endTime = m.clock
	return simResult{
		intValue: tx.endTime,
		reason:   fmt.Sprintf("precommit: ET=%d", tx.endTime),
	}
}

func (m *simModel) finish(id int) simResult {
	tx, ok := m.txs[id]
	if !ok {
		return simResult{err: ErrUnknownTx, reason: "reject: unknown transaction"}
	}
	if tx.status != simPrepared || tx.finished {
		return simResult{err: ErrInvalidStatus, reason: "reject: transaction is not unfinished prepared"}
	}
	tx.finished = true
	if len(m.deps[id]) > 0 {
		return simResult{ints: []int{}, reason: "finish: waiting for dependencies"}
	}
	order := m.commitReady(id)
	return simResult{ints: order, reason: fmt.Sprintf("finish: commit order=%v", order)}
}

func (m *simModel) abort(id int) simResult {
	tx, ok := m.txs[id]
	if !ok {
		return simResult{err: ErrUnknownTx, reason: "reject: unknown transaction"}
	}
	if tx.status != simActive && tx.status != simPrepared {
		return simResult{err: ErrInvalidStatus, reason: "reject: transaction is terminal"}
	}
	order := m.cascadeAbort(id)
	return simResult{ints: order, reason: fmt.Sprintf("abort: cascade=%v", order)}
}

func (m *simModel) read(id, key int) simResult {
	if _, ok := m.txs[id]; !ok {
		return simResult{err: ErrUnknownTx, reason: "reject: unknown transaction"}
	}
	if m.txs[id].status != simActive {
		return simResult{err: ErrInvalidStatus, reason: "reject: reader is not active"}
	}
	if key < 0 || key >= m.k {
		return simResult{err: ErrInvalidKey, reason: "reject: key out of range"}
	}

	reader := m.txs[id]
	for i := len(m.versions[key]) - 1; i >= 0; i-- {
		v := m.versions[key][i]
		if v.creator != noTx && m.txs[v.creator].status == simAborted {
			continue
		}
		if !m.startVisible(reader, v) {
			continue
		}
		if !m.endVisible(reader, v) {
			continue
		}

		dependency := false
		if v.creator != noTx && v.creator != id && m.txs[v.creator].status == simPrepared {
			m.addDependency(id, v.creator)
			dependency = true
		}
		return simResult{
			intValue: v.value,
			reason: fmt.Sprintf(
				"read key=%d value=%d creator=%d end=%s dependency=%t",
				key, v.value, v.creator, m.endReason(reader, v), dependency,
			),
		}
	}
	return simResult{err: ErrNoVisibleVersion, reason: "no visible version"}
}

func (m *simModel) write(id, key, value int) simResult {
	if _, ok := m.txs[id]; !ok {
		return simResult{err: ErrUnknownTx, reason: "reject: unknown transaction"}
	}
	if m.txs[id].status != simActive {
		return simResult{err: ErrInvalidStatus, reason: "reject: writer is not active"}
	}
	if key < 0 || key >= m.k {
		return simResult{err: ErrInvalidKey, reason: "reject: key out of range"}
	}

	writer := m.txs[id]
	var latest *simVersion
	for i := len(m.versions[key]) - 1; i >= 0; i-- {
		v := m.versions[key][i]
		if v.creator != noTx && m.txs[v.creator].status == simAborted {
			continue
		}
		latest = v
		break
	}

	if latest.creator == id {
		latest.value = value
		return simResult{reason: "write: update writer's own latest version"}
	}
	if m.effectiveEnder(latest) != noTx {
		m.cascadeAbort(id)
		return simResult{err: ErrWriteConflict, reason: "conflict: latest version has effective ender"}
	}

	creator := m.txs[latest.creator]
	if latest.creator != noTx && creator.status == simActive {
		m.cascadeAbort(id)
		return simResult{err: ErrWriteConflict, reason: "conflict: latest creator is active"}
	}
	creatorET := 0
	if latest.creator != noTx {
		creatorET = creator.endTime
	}
	if creatorET >= writer.readTime {
		m.cascadeAbort(id)
		return simResult{
			err:    ErrWriteConflict,
			reason: fmt.Sprintf("conflict: creator ET=%d >= writer RT=%d", creatorET, writer.readTime),
		}
	}

	latest.ender = id
	m.versions[key] = append(m.versions[key], &simVersion{value: value, creator: id, ender: noTx})
	dependency := false
	if creator != nil && creator.status == simPrepared {
		m.addDependency(id, latest.creator)
		dependency = true
	}
	return simResult{
		reason: fmt.Sprintf("write: append version value=%d dependency=%t", value, dependency),
	}
}

func (m *simModel) startVisible(reader *simTx, v *simVersion) bool {
	if v.creator == noTx || v.creator == reader.id {
		return true
	}
	creator := m.txs[v.creator]
	switch creator.status {
	case simActive, simAborted:
		return false
	case simPrepared, simCommitted:
		return creator.endTime < reader.readTime
	default:
		return false
	}
}

func (m *simModel) endVisible(reader *simTx, v *simVersion) bool {
	enderID := m.effectiveEnder(v)
	if enderID == noTx {
		return true
	}
	if enderID == reader.id {
		return false
	}
	ender := m.txs[enderID]
	switch ender.status {
	case simActive:
		return true
	case simPrepared, simCommitted:
		return reader.readTime < ender.endTime
	default:
		return false
	}
}

func (m *simModel) endReason(reader *simTx, v *simVersion) string {
	enderID := m.effectiveEnder(v)
	if enderID == noTx {
		return "none"
	}
	if enderID == reader.id {
		return "self-invisible"
	}
	ender := m.txs[enderID]
	switch ender.status {
	case simActive:
		return "active-visible"
	case simPrepared, simCommitted:
		return fmt.Sprintf("ender ET=%d reader RT=%d", ender.endTime, reader.readTime)
	default:
		return "aborted-none"
	}
}

func (m *simModel) effectiveEnder(v *simVersion) int {
	if v.ender == noTx {
		return noTx
	}
	if m.txs[v.ender].status == simAborted {
		return noTx
	}
	return v.ender
}

func (m *simModel) addDependency(dependent, dependency int) {
	if _, ok := m.deps[dependent][dependency]; ok {
		return
	}
	m.deps[dependent][dependency] = struct{}{}
	m.dependents[dependency][dependent] = struct{}{}
}

func (m *simModel) removeDependency(dependent, dependency int) {
	delete(m.deps[dependent], dependency)
	delete(m.dependents[dependency], dependent)
}

func (m *simModel) commitReady(first int) []int {
	order := make([]int, 0)
	ready := map[int]struct{}{first: {}}
	for len(ready) > 0 {
		id := smallestSimKey(ready)
		delete(ready, id)
		m.txs[id].status = simCommitted
		order = append(order, id)

		dependents := simMapKeys(m.dependents[id])
		sort.Ints(dependents)
		for _, dependentID := range dependents {
			m.removeDependency(dependentID, id)
			dependent := m.txs[dependentID]
			if dependent.status == simActive {
				continue
			}
			if dependent.status == simPrepared && dependent.finished && len(m.deps[dependentID]) == 0 {
				ready[dependentID] = struct{}{}
			}
		}
	}
	return order
}

func (m *simModel) cascadeAbort(root int) []int {
	queue := []int{root}
	seen := map[int]struct{}{root: {}}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for dependentID := range m.dependents[id] {
			if _, ok := seen[dependentID]; !ok {
				seen[dependentID] = struct{}{}
				queue = append(queue, dependentID)
			}
		}
	}

	aborted := make([]int, 0, len(seen))
	for id := range seen {
		m.txs[id].status = simAborted
		aborted = append(aborted, id)
		for dependency := range m.deps[id] {
			delete(m.dependents[dependency], id)
		}
	}
	sort.Ints(aborted)
	for id := range seen {
		m.deps[id] = make(map[int]struct{})
		m.dependents[id] = make(map[int]struct{})
	}
	return aborted
}

func smallestSimKey(values map[int]struct{}) int {
	result := 0
	first := true
	for value := range values {
		if first || value < result {
			result = value
			first = false
		}
	}
	return result
}

func simMapKeys(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed=%04d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			const keyCount = 3
			e, err := NewEngine(keyCount)
			if err != nil {
				t.Fatal(err)
			}
			m := newSimModel(keyCount)
			trace := fmt.Sprintf("random trace seed=%d keyCount=%d\n", seed, keyCount)
			var logSummary []string

			for step := 0; step < 70; step++ {
				op := generateOperation(rng, m)
				want := m.execute(op)
				got := executeEngine(e, op)
				trace += fmt.Sprintf(
					"step=%d input=%+v output engine={value:%d ints:%v err:%v} naive={value:%d ints:%v err:%v} basis=%q\n",
					step, op,
					got.intValue, got.ints, got.err,
					want.intValue, want.ints, want.err,
					want.reason,
				)
				logSummary = append(logSummary, fmt.Sprintf(
					"input=%s/%d/%d/%d output=(%d,%v,%v)/(%d,%v,%v) basis=%s",
					op.name, op.tx, op.key, op.value,
					got.intValue, got.ints, got.err,
					want.intValue, want.ints, want.err,
					want.reason,
				))
				assertSameResultWithTrace(t, trace, got, want)
				assertSameSnapshotWithTrace(t, trace, e, m)
				assertEngineInvariantsWithTrace(t, trace, e)
			}
			t.Logf("seed=%d cases=%d input/output/basis=%s", seed, len(logSummary), joinSummary(logSummary))
		})
	}
}

func joinSummary(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += " | "
		}
		result += value
	}
	return result
}

func generateOperation(rng *rand.Rand, m *simModel) simOperation {
	if len(m.txs) < 3 || rng.Intn(7) == 0 {
		return simOperation{name: "Begin"}
	}

	ids := simMapKeysSet(m.txs)
	id := ids[rng.Intn(len(ids))]
	if rng.Intn(15) == 0 {
		id = m.nextTx + rng.Intn(4)
	}

	key := rng.Intn(m.k)
	if rng.Intn(12) == 0 {
		key = m.k
	}

	if m.txs[id] == nil {
		return simOperation{
			name:  []string{"Read", "Write", "Precommit", "Finish", "Abort"}[rng.Intn(5)],
			tx:    id,
			key:   key,
			value: rng.Intn(100),
		}
	}

	switch m.txs[id].status {
	case simActive:
		switch rng.Intn(5) {
		case 0:
			return simOperation{name: "Read", tx: id, key: key}
		case 1:
			return simOperation{name: "Write", tx: id, key: key, value: rng.Intn(100)}
		case 2:
			return simOperation{name: "Precommit", tx: id}
		case 3:
			return simOperation{name: "Finish", tx: id}
		default:
			return simOperation{name: "Abort", tx: id}
		}
	case simPrepared:
		switch rng.Intn(5) {
		case 0:
			return simOperation{name: "Read", tx: id, key: key}
		case 1:
			return simOperation{name: "Write", tx: id, key: key, value: rng.Intn(100)}
		case 2:
			return simOperation{name: "Finish", tx: id}
		case 3:
			return simOperation{name: "Finish", tx: id}
		default:
			return simOperation{name: "Abort", tx: id}
		}
	default:
		return simOperation{
			name:  []string{"Read", "Write", "Precommit", "Finish", "Abort"}[rng.Intn(5)],
			tx:    id,
			key:   key,
			value: rng.Intn(100),
		}
	}
}

func simMapKeysSet(values map[int]*simTx) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func executeEngine(e *Engine, op simOperation) simResult {
	switch op.name {
	case "Begin":
		return simResult{intValue: e.Begin()}
	case "Read":
		value, err := e.Read(op.tx, op.key)
		return simResult{intValue: value, err: err}
	case "Write":
		return simResult{err: e.Write(op.tx, op.key, op.value)}
	case "Precommit":
		value, err := e.Precommit(op.tx)
		return simResult{intValue: value, err: err}
	case "Finish":
		values, err := e.Finish(op.tx)
		return simResult{ints: values, err: err}
	case "Abort":
		values, err := e.Abort(op.tx)
		return simResult{ints: values, err: err}
	default:
		panic("unknown operation")
	}
}

func assertSameResult(t *testing.T, got, want simResult) {
	t.Helper()
	if got.intValue != want.intValue {
		t.Fatalf("int result = %d, want %d", got.intValue, want.intValue)
	}
	if !sameError(got.err, want.err) {
		t.Fatalf("error = %v, want %v; basis=%s", got.err, want.err, want.reason)
	}
	if !sameInts(got.ints, want.ints) {
		t.Fatalf("slice result = %v, want %v; basis=%s", got.ints, want.ints, want.reason)
	}
}

func assertSameResultWithTrace(t *testing.T, trace string, got, want simResult) {
	t.Helper()
	if got.intValue != want.intValue {
		t.Fatalf("%sslice result mismatch: got=%d want=%d", trace, got.intValue, want.intValue)
	}
	if !sameError(got.err, want.err) {
		t.Fatalf("%serror mismatch: got=%v want=%v", trace, got.err, want.err)
	}
	if !sameInts(got.ints, want.ints) {
		t.Fatalf("%sslice result mismatch: got=%v want=%v", trace, got.ints, want.ints)
	}
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.Error() == want.Error()
}

func sameInts(got, want []int) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

func assertSameSnapshot(t *testing.T, e *Engine, m *simModel) {
	t.Helper()
	got := snapshotEngine(e)
	want := snapshotModel(m)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state mismatch\nengine=%#v\nnaive =%#v", got, want)
	}
}

func assertSameSnapshotWithTrace(t *testing.T, trace string, e *Engine, m *simModel) {
	t.Helper()
	got := snapshotEngine(e)
	want := snapshotModel(m)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%sstate mismatch\nengine=%#v\nnaive =%#v", trace, got, want)
	}
}

func snapshotModel(m *simModel) engineSnapshot {
	s := engineSnapshot{
		clock:  m.clock,
		tx:     make(map[int]txSnapshot),
		keys:   make([][]versionSnapshot, m.k),
		deps:   make(map[int][]int),
		depend: make(map[int][]int),
	}
	for id, tx := range m.txs {
		s.tx[id] = txSnapshot{
			readTime: tx.readTime,
			endTime:  tx.endTime,
			status:   TxStatus(tx.status),
			finished: tx.finished,
		}
	}
	for key, versions := range m.versions {
		for _, v := range versions {
			s.keys[key] = append(s.keys[key], versionSnapshot{
				value:   v.value,
				creator: v.creator,
				ender:   v.ender,
			})
		}
	}
	for id, values := range m.deps {
		for value := range values {
			s.deps[id] = append(s.deps[id], value)
		}
		sort.Ints(s.deps[id])
	}
	for id, values := range m.dependents {
		for value := range values {
			s.depend[id] = append(s.depend[id], value)
		}
		sort.Ints(s.depend[id])
	}
	return s
}

func assertEngineInvariants(t *testing.T, e *Engine) {
	t.Helper()
	for key := 0; key < e.keyCount; key++ {
		var latestNonGarbage *version
		for _, v := range e.versions[key] {
			if !e.isGarbage(v) {
				latestNonGarbage = v
			}
		}
		if latestNonGarbage == nil {
			t.Fatalf("key %d has no non-garbage version", key)
		}
		if e.effectiveEnder(latestNonGarbage) != noTx {
			t.Fatalf("latest non-garbage version for key %d has an effective ender", key)
		}
		for _, v := range e.versions[key] {
			if e.isGarbage(v) {
				continue
			}
			if v != latestNonGarbage && e.effectiveEnder(v) == noTx {
				t.Fatalf("older non-garbage version has no ender on key %d", key)
			}
		}
	}

	for id, tx := range e.transactions {
		if tx.status == Active || tx.status == Prepared {
			continue
		}
		if len(e.dependencies[id]) != 0 || len(e.dependents[id]) != 0 {
			t.Fatalf("terminal tx %d retained dependencies=%v dependents=%v", id, e.dependencies[id], e.dependents[id])
		}
	}
}

func assertEngineInvariantsWithTrace(t *testing.T, trace string, e *Engine) {
	t.Helper()
	for key := 0; key < e.keyCount; key++ {
		var latestNonGarbage *version
		for _, v := range e.versions[key] {
			if !e.isGarbage(v) {
				latestNonGarbage = v
			}
		}
		if latestNonGarbage == nil {
			t.Fatalf("%skey %d has no non-garbage version", trace, key)
		}
		if e.effectiveEnder(latestNonGarbage) != noTx {
			t.Fatalf("%slatest non-garbage version for key %d has an effective ender", trace, key)
		}
		for _, v := range e.versions[key] {
			if e.isGarbage(v) {
				continue
			}
			if v != latestNonGarbage && e.effectiveEnder(v) == noTx {
				t.Fatalf("%solder non-garbage version has no ender on key %d", trace, key)
			}
		}
	}
	for id, tx := range e.transactions {
		if tx.status == Active || tx.status == Prepared {
			continue
		}
		if len(e.dependencies[id]) != 0 || len(e.dependents[id]) != 0 {
			t.Fatalf("%sterminal tx %d retained dependencies=%v dependents=%v", trace, id, e.dependencies[id], e.dependents[id])
		}
	}
}
