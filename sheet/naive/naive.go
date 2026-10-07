// Package naive 是 sheet 服务的独立朴素参照实现，仅用于差分测试。
//
// 它与正式实现不共享任何状态机代码：记录用无序 map 存储、
// 冲突单元格靠全量收集后排序得到、栈直接用切片维护。
// 正确性优先于性能，以便作为“可肉眼审计”的对照模型。
package naive

import (
	"sort"

	"ontology/sheet"
)

type cell struct {
	value   int64
	empty   bool
	version int64
}

type change struct {
	oldValue int64
	oldEmpty bool
	newValue int64
	newEmpty bool
	version  int64
}

// record 用 map 存放变更，刻意不维护有序结构。
type record struct {
	changes map[string]change
}

func (r *record) sortedKeys() []string {
	keys := make([]string, 0, len(r.changes))
	for k := range r.changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Model 是朴素参照模型。语义与 sheet.Service 完全一致。
type Model struct {
	revision int64
	lastNow  int64
	depths   map[string]int
	cells    map[string]cell
	prot     map[string]string
	undo     map[string][]*record
	redo     map[string][]*record
}

// New 构造朴素模型；输入已保证合法（由测试生成器保证）。
func New(depths map[string]int) *Model {
	m := &Model{
		depths: make(map[string]int, len(depths)),
		cells:  make(map[string]cell),
		prot:   make(map[string]string),
		undo:   make(map[string][]*record, len(depths)),
		redo:   make(map[string][]*record, len(depths)),
	}
	for u, d := range depths {
		m.depths[u] = d
	}
	return m
}

func (m *Model) userOK(user string) bool {
	_, ok := m.depths[user]
	return ok
}

func nowOK(now int64) bool { return now >= 0 && now <= sheet.MaxNow }

func valueOK(v int64) bool { return v >= -sheet.MaxValue && v <= sheet.MaxValue }

func (m *Model) reject(code sheet.ErrCode) sheet.Result {
	return sheet.Result{OK: false, Code: code, Revision: m.revision}
}

func (m *Model) rejectCell(code sheet.ErrCode, cell string) sheet.Result {
	return sheet.Result{OK: false, Code: code, Cell: cell, Revision: m.revision}
}

func (m *Model) ok() sheet.Result {
	return sheet.Result{OK: true, Code: sheet.CodeOK, Revision: m.revision}
}

// Apply 与 sheet.Service.Apply 语义一致。
func (m *Model) Apply(user string, edits []sheet.Edit, now int64) sheet.Result {
	if !m.userOK(user) || len(edits) == 0 || len(edits) > sheet.MaxEdits || !nowOK(now) {
		return m.reject(sheet.CodeInvalidParam)
	}
	seen := make(map[string]bool, len(edits))
	for _, e := range edits {
		if e.Key == "" || seen[e.Key] || (!e.Clear && !valueOK(e.Value)) {
			return m.reject(sheet.CodeInvalidParam)
		}
		seen[e.Key] = true
	}
	if now < m.lastNow {
		return m.reject(sheet.CodeClockRollback)
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if owner, ok := m.prot[k]; ok && owner != user {
			return m.rejectCell(sheet.CodeProtected, k)
		}
	}
	rec := &record{changes: make(map[string]change)}
	for _, e := range edits {
		cur, exists := m.cells[e.Key]
		curEmpty := !exists || cur.empty
		if e.Clear {
			if curEmpty {
				continue
			}
		} else if !curEmpty && cur.value == e.Value {
			continue
		}
		rec.changes[e.Key] = change{
			oldValue: cur.value,
			oldEmpty: curEmpty,
			newValue: e.Value,
			newEmpty: e.Clear,
		}
	}
	if len(rec.changes) == 0 {
		return m.reject(sheet.CodeNoChange)
	}
	m.revision++
	for k, c := range rec.changes {
		c.version = m.revision
		rec.changes[k] = c
		m.cells[k] = cell{value: c.newValue, empty: c.newEmpty, version: m.revision}
	}
	m.undo[user] = append(m.undo[user], rec)
	if len(m.undo[user]) > m.depths[user] {
		m.undo[user] = m.undo[user][1:]
	}
	m.redo[user] = nil
	m.lastNow = now
	return m.ok()
}

// topMismatch 返回记录中按键排序第一个版本不符的单元格。
func (m *Model) topMismatch(rec *record) (string, bool) {
	for _, k := range rec.sortedKeys() {
		want := rec.changes[k].version
		var got int64
		if c, ok := m.cells[k]; ok {
			got = c.version
		}
		if got != want {
			return k, true
		}
	}
	return "", false
}

// topProtected 返回记录中按键排序第一个被他人保护的单元格。
func (m *Model) topProtected(user string, rec *record) (string, bool) {
	for _, k := range rec.sortedKeys() {
		if owner, ok := m.prot[k]; ok && owner != user {
			return k, true
		}
	}
	return "", false
}

// Undo 与 sheet.Service.Undo 语义一致。
func (m *Model) Undo(user string, now int64) sheet.Result {
	if !m.userOK(user) || !nowOK(now) {
		return m.reject(sheet.CodeInvalidParam)
	}
	if now < m.lastNow {
		return m.reject(sheet.CodeClockRollback)
	}
	stack := m.undo[user]
	if len(stack) == 0 {
		return m.reject(sheet.CodeEmptyStack)
	}
	rec := stack[len(stack)-1]
	if k, bad := m.topMismatch(rec); bad {
		m.undo[user] = stack[:len(stack)-1]
		return sheet.Result{OK: false, Code: sheet.CodeOverwritten, Cell: k, Dropped: true, Revision: m.revision}
	}
	if k, bad := m.topProtected(user, rec); bad {
		return m.rejectCell(sheet.CodeProtected, k)
	}
	m.revision++
	newRec := &record{changes: make(map[string]change, len(rec.changes))}
	for k, c := range rec.changes {
		m.cells[k] = cell{value: c.oldValue, empty: c.oldEmpty, version: m.revision}
		c.version = m.revision
		newRec.changes[k] = c
	}
	m.undo[user] = stack[:len(stack)-1]
	m.redo[user] = append(m.redo[user], newRec)
	m.lastNow = now
	return m.ok()
}

// Redo 与 sheet.Service.Redo 语义一致。
func (m *Model) Redo(user string, now int64) sheet.Result {
	if !m.userOK(user) || !nowOK(now) {
		return m.reject(sheet.CodeInvalidParam)
	}
	if now < m.lastNow {
		return m.reject(sheet.CodeClockRollback)
	}
	stack := m.redo[user]
	if len(stack) == 0 {
		return m.reject(sheet.CodeEmptyStack)
	}
	rec := stack[len(stack)-1]
	if k, bad := m.topMismatch(rec); bad {
		m.redo[user] = stack[:len(stack)-1]
		return sheet.Result{OK: false, Code: sheet.CodeOverwritten, Cell: k, Dropped: true, Revision: m.revision}
	}
	if k, bad := m.topProtected(user, rec); bad {
		return m.rejectCell(sheet.CodeProtected, k)
	}
	m.revision++
	newRec := &record{changes: make(map[string]change, len(rec.changes))}
	for k, c := range rec.changes {
		m.cells[k] = cell{value: c.newValue, empty: c.newEmpty, version: m.revision}
		c.version = m.revision
		newRec.changes[k] = c
	}
	m.redo[user] = stack[:len(stack)-1]
	m.undo[user] = append(m.undo[user], newRec)
	if len(m.undo[user]) > m.depths[user] {
		m.undo[user] = m.undo[user][1:]
	}
	m.lastNow = now
	return m.ok()
}

// Protect 与 sheet.Service.Protect 语义一致。
func (m *Model) Protect(owner, cellKey string, now int64) sheet.Result {
	if !m.userOK(owner) || cellKey == "" || !nowOK(now) {
		return m.reject(sheet.CodeInvalidParam)
	}
	if now < m.lastNow {
		return m.reject(sheet.CodeClockRollback)
	}
	if cur, ok := m.prot[cellKey]; ok {
		if cur != owner {
			return m.rejectCell(sheet.CodeProtected, cellKey)
		}
		m.lastNow = now
		return m.ok()
	}
	m.prot[cellKey] = owner
	m.lastNow = now
	return m.ok()
}

// Unprotect 与 sheet.Service.Unprotect 语义一致。
func (m *Model) Unprotect(user, cellKey string, now int64) sheet.Result {
	if !m.userOK(user) || cellKey == "" || !nowOK(now) {
		return m.reject(sheet.CodeInvalidParam)
	}
	if now < m.lastNow {
		return m.reject(sheet.CodeClockRollback)
	}
	cur, ok := m.prot[cellKey]
	if !ok {
		m.lastNow = now
		return m.ok()
	}
	if cur != user {
		return m.rejectCell(sheet.CodeProtected, cellKey)
	}
	delete(m.prot, cellKey)
	m.lastNow = now
	return m.ok()
}

// Cell 查询单元格值与版本。
func (m *Model) Cell(key string) sheet.CellState {
	c, ok := m.cells[key]
	if !ok {
		return sheet.CellState{Empty: true}
	}
	return sheet.CellState{Value: c.value, Empty: c.empty, Version: c.version}
}

// History 查询用户历史栈信息。
func (m *Model) History(user string) (sheet.HistoryInfo, bool) {
	if !m.userOK(user) {
		return sheet.HistoryInfo{}, false
	}
	info := sheet.HistoryInfo{UndoCount: len(m.undo[user]), RedoCount: len(m.redo[user])}
	if n := len(m.undo[user]); n > 0 {
		info.UndoTop = m.undo[user][n-1].sortedKeys()
	}
	if n := len(m.redo[user]); n > 0 {
		info.RedoTop = m.redo[user][n-1].sortedKeys()
	}
	return info, true
}

// Revision 返回当前全局修订号。
func (m *Model) Revision() int64 { return m.revision }

// Protector 返回单元格保护者。
func (m *Model) Protector(cellKey string) (string, bool) {
	owner, ok := m.prot[cellKey]
	return owner, ok
}
