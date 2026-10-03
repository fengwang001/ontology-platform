package ontology

import (
	"fmt"
	"sort"
)

type naiveVersion struct {
	cs    int
	ps    int
	ss    int
	value int
}

type naiveTransaction struct {
	snapshot int
	readSet  map[int]*naiveVersion
	writeSet map[int]int
	active   bool
}

type naiveModel struct {
	keys         int
	history      int
	n            int
	nextTx       int
	versions     map[int][]*naiveVersion
	allVersions  map[int][]*naiveVersion
	evicted      []*naiveVersion
	transactions map[int]*naiveTransaction
	committed    map[int]*naiveTransaction
	logs         []string
}

type versionSnapshot struct {
	CS    int
	PS    int
	SS    int
	Value int
}

type modelSnapshot struct {
	N            int
	NextTx       int
	Chains       map[int][]versionSnapshot
	Evicted      []versionSnapshot
	Transactions map[int][2]map[int]int
}

func itoa(value int) string {
	return fmt.Sprint(value)
}

func maxInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}

func minInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func namedError(name string) error {
	switch name {
	case "ErrTxNotFound":
		return ErrTxNotFound
	case "ErrTxFinished":
		return ErrTxFinished
	case "ErrKeyOutOfRange":
		return ErrKeyOutOfRange
	case "ErrSnapshotTooOld":
		return ErrSnapshotTooOld
	default:
		return nil
	}
}

func versionMap(tx *transaction) map[int]int {
	result := make(map[int]int, len(tx.readSet))
	for key, selected := range tx.readSet {
		result[key] = selected.cs
	}
	return result
}

func naiveVersionMap(tx *naiveTransaction) map[int]int {
	result := make(map[int]int, len(tx.readSet))
	for key, selected := range tx.readSet {
		result[key] = selected.cs
	}
	return result
}

func actualSnapshot(a *Authenticator) modelSnapshot {
	snapshot := modelSnapshot{
		N:            a.state.commitNumber,
		NextTx:       a.state.nextTxID,
		Chains:       make(map[int][]versionSnapshot, a.state.keys),
		Evicted:      nil,
		Transactions: make(map[int][2]map[int]int),
	}
	for key, chain := range a.state.versions {
		for _, selected := range chain {
			snapshot.Chains[key] = append(snapshot.Chains[key], versionSnapshot{
				CS:    selected.cs,
				PS:    selected.ps,
				SS:    selected.ss,
				Value: selected.value,
			})
		}
	}
	activeByID := make(map[*version]int)
	for txID, tx := range a.state.transactions {
		for _, selected := range tx.readSet {
			activeByID[selected] = txID
		}
	}
	chainObjects := make(map[*version]bool)
	for _, chain := range a.state.versions {
		for _, selected := range chain {
			chainObjects[selected] = true
		}
	}
	ids := make([]int, 0)
	for selected, txID := range activeByID {
		if !chainObjects[selected] {
			ids = append(ids, txID)
		}
	}
	sort.Ints(ids)
	seen := make(map[*version]bool)
	for _, txID := range ids {
		keys := make([]int, 0)
		for key, selected := range a.state.transactions[txID].readSet {
			if !chainObjects[selected] && !seen[selected] {
				keys = append(keys, key)
			}
		}
		sort.Ints(keys)
		for _, key := range keys {
			selected := a.state.transactions[txID].readSet[key]
			if seen[selected] {
				continue
			}
			seen[selected] = true
			snapshot.Evicted = append(snapshot.Evicted, versionSnapshot{
				CS:    selected.cs,
				PS:    selected.ps,
				SS:    selected.ss,
				Value: selected.value,
			})
		}
	}
	for txID, tx := range a.state.transactions {
		status := 0
		if !tx.active {
			status = 1
		}
		entries := tx.writeSet
		writeCopy := make(map[int]int, len(entries)+2)
		for key, value := range entries {
			writeCopy[key] = value
		}
		writeCopy[-1] = tx.snapshot
		writeCopy[-2] = status
		snapshot.Transactions[txID] = [2]map[int]int{writeCopy, versionMap(tx)}
	}
	sort.Slice(snapshot.Evicted, func(i int, j int) bool {
		if snapshot.Evicted[i].CS != snapshot.Evicted[j].CS {
			return snapshot.Evicted[i].CS < snapshot.Evicted[j].CS
		}
		if snapshot.Evicted[i].Value != snapshot.Evicted[j].Value {
			return snapshot.Evicted[i].Value < snapshot.Evicted[j].Value
		}
		if snapshot.Evicted[i].PS != snapshot.Evicted[j].PS {
			return snapshot.Evicted[i].PS < snapshot.Evicted[j].PS
		}
		return snapshot.Evicted[i].SS < snapshot.Evicted[j].SS
	})
	return snapshot
}

func (m *naiveModel) snapshot() modelSnapshot {
	snapshot := modelSnapshot{
		N:            m.n,
		NextTx:       m.nextTx,
		Chains:       make(map[int][]versionSnapshot, m.keys),
		Transactions: make(map[int][2]map[int]int),
	}
	for key, chain := range m.versions {
		for _, selected := range chain {
			snapshot.Chains[key] = append(snapshot.Chains[key], versionSnapshot{
				CS:    selected.cs,
				PS:    selected.ps,
				SS:    selected.ss,
				Value: selected.value,
			})
		}
		sort.Slice(snapshot.Chains[key], func(i int, j int) bool {
			return snapshot.Chains[key][i].CS < snapshot.Chains[key][j].CS
		})
	}
	referenced := make(map[*naiveVersion]bool)
	for _, tx := range m.transactions {
		for _, selected := range tx.readSet {
			referenced[selected] = true
		}
	}
	for _, selected := range m.evicted {
		if !referenced[selected] {
			continue
		}
		snapshot.Evicted = append(snapshot.Evicted, versionSnapshot{
			CS:    selected.cs,
			PS:    selected.ps,
			SS:    selected.ss,
			Value: selected.value,
		})
	}
	sort.Slice(snapshot.Evicted, func(i int, j int) bool {
		if snapshot.Evicted[i].CS != snapshot.Evicted[j].CS {
			return snapshot.Evicted[i].CS < snapshot.Evicted[j].CS
		}
		if snapshot.Evicted[i].Value != snapshot.Evicted[j].Value {
			return snapshot.Evicted[i].Value < snapshot.Evicted[j].Value
		}
		if snapshot.Evicted[i].PS != snapshot.Evicted[j].PS {
			return snapshot.Evicted[i].PS < snapshot.Evicted[j].PS
		}
		return snapshot.Evicted[i].SS < snapshot.Evicted[j].SS
	})
	for txID, tx := range m.transactions {
		status := 0
		if !tx.active {
			status = 1
		}
		writeCopy := map[int]int{-1: tx.snapshot, -2: status}
		for key, value := range tx.writeSet {
			writeCopy[key] = value
		}
		snapshot.Transactions[txID] = [2]map[int]int{writeCopy, naiveVersionMap(tx)}
	}
	return snapshot
}

func newNaiveModel(keys int, history int) *naiveModel {
	m := &naiveModel{
		keys:         keys,
		history:      history,
		nextTx:       1,
		versions:     make(map[int][]*naiveVersion),
		allVersions:  make(map[int][]*naiveVersion),
		transactions: make(map[int]*naiveTransaction),
		committed:    make(map[int]*naiveTransaction),
	}
	for key := 0; key < keys; key++ {
		initial := &naiveVersion{cs: 0, ps: 0, ss: infinity, value: 0}
		m.versions[key] = []*naiveVersion{initial}
		m.allVersions[key] = []*naiveVersion{initial}
	}
	return m
}

func (m *naiveModel) begin() int {
	txID := m.nextTx
	m.nextTx++
	m.transactions[txID] = &naiveTransaction{
		snapshot: m.n,
		readSet:  make(map[int]*naiveVersion),
		writeSet: make(map[int]int),
		active:   true,
	}
	m.logs = append(m.logs, "Begin => "+itoa(txID)+" snapshot="+itoa(m.n))
	return txID
}

func (m *naiveModel) active(txID int) (*naiveTransaction, string) {
	tx, ok := m.transactions[txID]
	if !ok {
		return nil, "ErrTxNotFound"
	}
	if !tx.active {
		return nil, "ErrTxFinished"
	}
	return tx, ""
}

func (m *naiveModel) read(txID int, key int) (int, error) {
	tx, failure := m.active(txID)
	if failure != "" {
		m.logs = append(m.logs, "Read rejected "+failure)
		return 0, namedError(failure)
	}
	if key < 0 || key >= m.keys {
		m.logs = append(m.logs, "Read rejected ErrKeyOutOfRange")
		return 0, ErrKeyOutOfRange
	}
	if value, ok := tx.writeSet[key]; ok {
		m.logs = append(m.logs, "Read buffered value="+itoa(value))
		return value, nil
	}
	if selected, ok := tx.readSet[key]; ok {
		m.logs = append(m.logs, "Read first-selected value="+itoa(selected.value))
		return selected.value, nil
	}
	chain := m.versions[key]
	if chain[0].cs > tx.snapshot {
		m.logs = append(m.logs, "Read rejected ErrSnapshotTooOld")
		return 0, ErrSnapshotTooOld
	}
	var selected *naiveVersion
	for _, candidate := range chain {
		if candidate.cs <= tx.snapshot {
			selected = candidate
		}
	}
	tx.readSet[key] = selected
	m.logs = append(m.logs, "Read version cs="+itoa(selected.cs)+" value="+itoa(selected.value))
	return selected.value, nil
}

func (m *naiveModel) write(txID int, key int, value int) error {
	tx, failure := m.active(txID)
	if failure != "" {
		m.logs = append(m.logs, "Write rejected "+failure)
		return namedError(failure)
	}
	if key < 0 || key >= m.keys {
		m.logs = append(m.logs, "Write rejected ErrKeyOutOfRange")
		return ErrKeyOutOfRange
	}
	tx.writeSet[key] = value
	m.logs = append(m.logs, "Write buffered key="+itoa(key)+" value="+itoa(value))
	return nil
}

func (m *naiveModel) commit(txID int) (int, AbortReason, error) {
	tx, failure := m.active(txID)
	if failure != "" {
		m.logs = append(m.logs, "Commit rejected "+failure)
		return 0, AbortNone, namedError(failure)
	}

	keys := make([]int, 0, len(tx.writeSet))
	for key := range tx.writeSet {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	latest := make(map[int]*naiveVersion, len(keys))
	for _, key := range keys {
		chain := m.versions[key]
		latest[key] = chain[len(chain)-1]
		if latest[key].cs > tx.snapshot {
			tx.active = false
			m.logs = append(m.logs, "Commit aborted WW latest.cs="+itoa(latest[key].cs)+" snapshot="+itoa(tx.snapshot))
			return 0, AbortWriteWriteConflict, ErrWriteWriteConflict
		}
	}

	c := m.n + 1
	eta := 0
	pi := c
	readKeys := make([]int, 0, len(tx.readSet))
	for key := range tx.readSet {
		readKeys = append(readKeys, key)
	}
	sort.Ints(readKeys)
	for _, key := range readKeys {
		selected := tx.readSet[key]
		eta = maxInt(eta, selected.cs)
		pi = minInt(pi, selected.ss)
	}
	for _, key := range keys {
		selected := latest[key]
		if readVersion, ok := tx.readSet[key]; ok {
			selected = readVersion
		}
		eta = maxInt(eta, selected.cs, selected.ps)
	}
	m.logs = append(m.logs, "Commit basis c="+itoa(c)+" eta="+itoa(eta)+" pi="+itoa(pi))
	if pi <= eta {
		tx.active = false
		m.logs = append(m.logs, "Commit aborted SSN exclusion window")
		return 0, AbortSerializationSafetyNet, ErrSerializationSafetyNet
	}

	m.n = c
	for _, selected := range tx.readSet {
		selected.ps = maxInt(selected.ps, c)
	}
	for _, key := range keys {
		previous := latest[key]
		previous.ss = pi
		newVersion := &naiveVersion{cs: c, ps: 0, ss: infinity, value: tx.writeSet[key]}
		m.versions[key] = append(m.versions[key], newVersion)
		m.allVersions[key] = append(m.allVersions[key], newVersion)
		if len(m.versions[key]) > m.history {
			m.evicted = append(m.evicted, m.versions[key][0])
			m.versions[key] = m.versions[key][1:]
		}
	}
	tx.active = false
	committed := *tx
	m.committed[c] = &committed
	m.logs = append(m.logs, "Commit accepted c="+itoa(c))
	return c, AbortNone, nil
}

func (m *naiveModel) abort(txID int) error {
	tx, failure := m.active(txID)
	if failure != "" {
		m.logs = append(m.logs, "Abort rejected "+failure)
		return namedError(failure)
	}
	tx.active = false
	m.logs = append(m.logs, "Abort accepted")
	return nil
}

type graphEdge struct {
	from int
	to   int
	kind string
}

func (m *naiveModel) dependencyGraph() []graphEdge {
	commits := make([]int, 0, len(m.committed))
	for commitID := range m.committed {
		commits = append(commits, commitID)
	}
	sort.Ints(commits)
	edges := make([]graphEdge, 0)
	seen := make(map[graphEdge]bool)
	addEdge := func(from int, to int, kind string) {
		edge := graphEdge{from: from, to: to, kind: kind}
		if from != to && !seen[edge] {
			seen[edge] = true
			edges = append(edges, edge)
		}
	}
	for key := 0; key < m.keys; key++ {
		for i := 1; i < len(m.allVersions[key]); i++ {
			addEdge(m.allVersions[key][i-1].cs, m.allVersions[key][i].cs, "WW")
		}
		for _, readerCommit := range commits {
			reader := m.committed[readerCommit]
			selected, ok := reader.readSet[key]
			if !ok {
				continue
			}
			if selected.cs > 0 {
				addEdge(selected.cs, readerCommit, "WR")
			}
			if _, wrote := reader.writeSet[key]; wrote {
				continue
			}
			nextCommit := 0
			for _, version := range m.allVersions[key] {
				if version.cs > readerCommit && (nextCommit == 0 || version.cs < nextCommit) {
					nextCommit = version.cs
				}
			}
			if nextCommit != 0 {
				addEdge(nextCommit, readerCommit, "RW")
			}
		}
	}
	return edges
}

func graphHasCycle(edges []graphEdge) bool {
	nodes := make(map[int][]int)
	for _, edge := range edges {
		nodes[edge.from] = append(nodes[edge.from], edge.to)
		nodes[edge.to] = nodes[edge.to]
	}
	const (
		white = iota
		gray
		black
	)
	color := make(map[int]int)
	var visit func(int) bool
	visit = func(node int) bool {
		color[node] = gray
		for _, next := range nodes[node] {
			if color[next] == gray || (color[next] == white && visit(next)) {
				return true
			}
		}
		color[node] = black
		return false
	}
	for node := range nodes {
		if color[node] == white && visit(node) {
			return true
		}
	}
	return false
}
