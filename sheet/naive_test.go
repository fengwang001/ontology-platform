package sheet

// 本文件是独立于正式实现的朴素参考模型，仅用于随机对照测试。
// 它按规范字面直译，刻意采用最直白（且低效的写法，
// 例如每次都重新扫描与排序），与正式实现的高性能结构相互印证。

import "sort"

type nCell struct {
	value   int64
	set     bool
	version int64
}

type nChange struct {
	key            string
	oldV, newV     int64
	oldSet, newSet bool
}

type nRecord struct {
	changes []nChange
	version int64
}

type nUser struct {
	depth      int
	undo, redo []nRecord
}

type naiveService struct {
	cells   map[string]*nCell
	prot    map[string]string
	users   map[string]*nUser
	rev     int64
	last    int64
	hasLast bool
}

func newNaiveService(depths map[string]int) *naiveService {
	n := &naiveService{
		cells: map[string]*nCell{},
		prot:  map[string]string{},
		users: map[string]*nUser{},
	}
	for u, d := range depths {
		n.users[u] = &nUser{depth: d}
	}
	return n
}

func (n *naiveService) verOf(key string) int64 {
	if c, ok := n.cells[key]; ok {
		return c.version
	}
	return 0
}

// 朴素模型自带独立的参数校验，不复用正式实现的校验函数，
// 保证对照测试能发现校验逻辑本身的缺陷。
func nValidNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func nValidateEdits(edits []Edit) bool {
	if len(edits) < 1 || len(edits) > 50 {
		return false
	}
	seen := map[string]bool{}
	for _, e := range edits {
		if e.Key == "" || seen[e.Key] {
			return false
		}
		seen[e.Key] = true
		if !e.Clear && (e.Value < -1_000_000_000_000_000 || e.Value > 1_000_000_000_000_000) {
			return false
		}
	}
	return true
}

func (n *naiveService) clockOK(now int64) bool { return !n.hasLast || now >= n.last }

func (n *naiveService) accept(now int64) { n.last, n.hasLast = now, true }

func sortedKeysOf(changes []nChange) []string {
	keys := make([]string, len(changes))
	for i, c := range changes {
		keys[i] = c.key
	}
	sort.Strings(keys)
	return keys
}

func (n *naiveService) Apply(user string, edits []Edit, now int64) Result {
	u, ok := n.users[user]
	if !ok || user == "" || !nValidateEdits(edits) || !nValidNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !n.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	var keys []string
	for _, e := range edits {
		keys = append(keys, e.Key)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if o, ok := n.prot[k]; ok && o != user {
			return Result{Code: ErrProtected, Cell: k}
		}
	}
	var valid []Edit
	for _, e := range edits {
		c := n.cells[e.Key]
		if e.Clear {
			if c == nil || !c.set {
				continue
			}
		} else if c != nil && c.set && c.value == e.Value {
			continue
		}
		valid = append(valid, e)
	}
	if len(valid) == 0 {
		return Result{Code: ErrNoChange}
	}
	n.rev++
	rec := nRecord{version: n.rev}
	for _, e := range valid {
		ch := nChange{key: e.Key, newV: e.Value, newSet: !e.Clear}
		if c := n.cells[e.Key]; c != nil {
			ch.oldV, ch.oldSet = c.value, c.set
		}
		n.cells[e.Key] = &nCell{value: e.Value, set: !e.Clear, version: n.rev}
		rec.changes = append(rec.changes, ch)
	}
	u.undo = append(u.undo, rec)
	if len(u.undo) > u.depth {
		u.undo = u.undo[len(u.undo)-u.depth:]
	}
	u.redo = nil
	n.accept(now)
	return Result{Code: OK, Revision: n.rev}
}

func (n *naiveService) Undo(user string, now int64) Result {
	u, ok := n.users[user]
	if !ok || user == "" || !nValidNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !n.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if len(u.undo) == 0 {
		return Result{Code: ErrEmptyStack}
	}
	rec := u.undo[len(u.undo)-1]
	for _, k := range sortedKeysOf(rec.changes) {
		if n.verOf(k) != rec.version {
			u.undo = u.undo[:len(u.undo)-1]
			return Result{Code: ErrOverwritten, Cell: k, Popped: true}
		}
	}
	for _, k := range sortedKeysOf(rec.changes) {
		if o, ok := n.prot[k]; ok && o != user {
			return Result{Code: ErrProtected, Cell: k}
		}
	}
	n.rev++
	for _, c := range rec.changes {
		n.cells[c.key] = &nCell{value: c.oldV, set: c.oldSet, version: n.rev}
	}
	u.undo = u.undo[:len(u.undo)-1]
	rec.version = n.rev
	u.redo = append(u.redo, rec)
	n.accept(now)
	return Result{Code: OK, Revision: n.rev}
}

func (n *naiveService) Redo(user string, now int64) Result {
	u, ok := n.users[user]
	if !ok || user == "" || !nValidNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !n.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if len(u.redo) == 0 {
		return Result{Code: ErrEmptyStack}
	}
	rec := u.redo[len(u.redo)-1]
	for _, k := range sortedKeysOf(rec.changes) {
		if n.verOf(k) != rec.version {
			u.redo = u.redo[:len(u.redo)-1]
			return Result{Code: ErrOverwritten, Cell: k, Popped: true}
		}
	}
	for _, k := range sortedKeysOf(rec.changes) {
		if o, ok := n.prot[k]; ok && o != user {
			return Result{Code: ErrProtected, Cell: k}
		}
	}
	n.rev++
	for _, c := range rec.changes {
		n.cells[c.key] = &nCell{value: c.newV, set: c.newSet, version: n.rev}
	}
	u.redo = u.redo[:len(u.redo)-1]
	rec.version = n.rev
	u.undo = append(u.undo, rec)
	if len(u.undo) > u.depth {
		u.undo = u.undo[len(u.undo)-u.depth:]
	}
	n.accept(now)
	return Result{Code: OK, Revision: n.rev}
}

func (n *naiveService) Protect(owner, cell string, now int64) Result {
	if _, ok := n.users[owner]; !ok || owner == "" || cell == "" || !nValidNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !n.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if cur, ok := n.prot[cell]; ok {
		if cur != owner {
			return Result{Code: ErrProtected, Cell: cell}
		}
		n.accept(now)
		return Result{Code: OK}
	}
	n.prot[cell] = owner
	n.accept(now)
	return Result{Code: OK}
}

func (n *naiveService) Unprotect(user, cell string, now int64) Result {
	if _, ok := n.users[user]; !ok || user == "" || cell == "" || !nValidNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !n.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if cur, ok := n.prot[cell]; ok {
		if cur != user {
			return Result{Code: ErrProtected, Cell: cell}
		}
		delete(n.prot, cell)
	}
	n.accept(now)
	return Result{Code: OK}
}

func (n *naiveService) Cell(key string) (CellInfo, bool) {
	if key == "" {
		return CellInfo{}, false
	}
	if c, ok := n.cells[key]; ok {
		return CellInfo{Value: c.value, Set: c.set, Version: c.version}, true
	}
	return CellInfo{}, true
}

func (n *naiveService) History(user string) (HistoryInfo, bool) {
	u, ok := n.users[user]
	if !ok || user == "" {
		return HistoryInfo{}, false
	}
	info := HistoryInfo{UndoCount: len(u.undo), RedoCount: len(u.redo)}
	if len(u.undo) > 0 {
		info.UndoTop = sortedKeysOf(u.undo[len(u.undo)-1].changes)
	}
	if len(u.redo) > 0 {
		info.RedoTop = sortedKeysOf(u.redo[len(u.redo)-1].changes)
	}
	return info, true
}
