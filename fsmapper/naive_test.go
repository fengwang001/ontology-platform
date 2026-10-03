package fsmapper

// 本文件提供严格按题目四步规则“逐步”重写的朴素参考实现，
// 与生产代码不共享任何映射函数（仅复用哨兵错误），用于随机对照。

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type nEntry struct {
	parent  int64
	src     string
	mapped  string
	isDir   bool
	pathLen int
	kids    []int64
}

type naive struct {
	mb, mp, me int
	entries    map[int64]*nEntry
	dirs       map[int64]map[string]int64
	keys       map[int64]map[string]int64
	nextID     int64
}

func newNaive(mb, mp, me int) *naive {
	n := &naive{
		mb:      mb,
		mp:      mp,
		me:      me,
		entries: map[int64]*nEntry{},
		dirs:    map[int64]map[string]int64{},
		keys:    map[int64]map[string]int64{},
		nextID:  1,
	}
	n.entries[0] = &nEntry{isDir: true}
	n.dirs[0] = map[string]int64{}
	n.keys[0] = map[string]int64{}
	return n
}

func nValid(s string) bool {
	if s == "" || strings.ContainsRune(s, '/') || strings.IndexByte(s, 0) >= 0 {
		return false
	}
	return utf8.ValidString(s)
}

const nHex = "0123456789ABCDEF"

func nEscByte(b byte) string {
	return "%" + string(nHex[b>>4]) + string(nHex[b&0xF])
}

func nStep1(src string) string {
	var out string
	for _, r := range src {
		if r < utf8.RuneSelf {
			b := byte(r)
			need := b == '%' || b < 0x20
			if !need {
				switch b {
				case '<', '>', ':', '"', '\\', '|', '?', '*':
					need = true
				}
			}
			if need {
				out += nEscByte(b)
				continue
			}
		}
		out += string(r)
	}
	return out
}

func nStep2(s string) string {
	switch s[len(s)-1] {
	case '.':
		return s[:len(s)-1] + "%2E"
	case ' ':
		return s[:len(s)-1] + "%20"
	}
	return s
}

func nFold(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func nReserved(stem string) bool {
	switch stem {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(stem) == 4 && (stem[:3] == "com" || stem[:3] == "lpt") &&
		stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}

func nStep3(s string) string {
	stem := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		stem = s[:i]
	}
	if nReserved(nFold(stem)) {
		return nEscByte(s[0]) + s[1:]
	}
	return s
}

func nFNV(s string) string {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08X", h)
}

func nTokEnd(s string, off int) int {
	if s[off] == '%' && off+2 < len(s) &&
		strings.IndexByte(nHex, s[off+1]) >= 0 &&
		strings.IndexByte(nHex, s[off+2]) >= 0 {
		return off + 3
	}
	_, sz := utf8.DecodeRuneInString(s[off:])
	return off + sz
}

func nStep4(s string, mb int) string {
	if len(s) <= mb {
		return s
	}
	limit := mb - 9
	end := 0
	for end < len(s) {
		nxt := nTokEnd(s, end)
		if nxt > limit {
			break
		}
		end = nxt
	}
	return s[:end] + "~" + nFNV(s)
}

func nBase(src string, mb int) string {
	s := nStep1(src)
	s = nStep2(s)
	s = nStep3(s)
	s = nStep4(s, mb)
	return s
}

func nSplit(s string) (string, string) {
	i := strings.LastIndexByte(s, '.')
	if i > 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

func nShorten(base string, limit int) (string, bool) {
	end := 0
	for end < len(base) {
		nxt := nTokEnd(base, end)
		if nxt > limit {
			break
		}
		end = nxt
	}
	if end == 0 {
		return "", false
	}
	return base[:end], true
}

func (n *naive) allocate(parent int64, src string) (string, error) {
	t0 := nBase(src, n.mb)
	if _, taken := n.keys[parent][nFold(t0)]; !taken {
		return t0, nil
	}
	base, ext := nSplit(t0)
	base = trailingNumberSuffix.ReplaceAllString(base, "")
	for num := 2; ; num++ {
		suf := "~" + fmt.Sprint(num)
		lim := n.mb - len(suf) - len(ext)
		if lim < 0 {
			return "", ErrCannotFit
		}
		b, ok := nShorten(base, lim)
		if !ok {
			return "", ErrCannotFit
		}
		cand := b + suf + ext
		if _, taken := n.keys[parent][nFold(cand)]; !taken {
			return cand, nil
		}
	}
}

func (n *naive) subtreeOK(root int64, delta int) bool {
	st := []int64{root}
	for len(st) > 0 {
		c := st[len(st)-1]
		st = st[:len(st)-1]
		if n.entries[c].pathLen+delta > n.mp {
			return false
		}
		st = append(st, n.entries[c].kids...)
	}
	return true
}

func (n *naive) bump(root int64, delta int) {
	st := []int64{root}
	for len(st) > 0 {
		c := st[len(st)-1]
		st = st[:len(st)-1]
		n.entries[c].pathLen += delta
		st = append(st, n.entries[c].kids...)
	}
}

func (n *naive) Add(parent int64, src string, isDir bool) (int64, error) {
	if !nValid(src) {
		return 0, ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.isDir {
		return 0, ErrNoParent
	}
	if _, ok := n.dirs[parent][src]; ok {
		return 0, ErrExists
	}
	if len(n.dirs[parent]) >= n.me {
		return 0, ErrFull
	}
	mapped, err := n.allocate(parent, src)
	if err != nil {
		return 0, err
	}
	pl := p.pathLen + 1 + len(mapped)
	if pl > n.mp {
		return 0, ErrPathTooLong
	}
	id := n.nextID
	n.nextID++
	n.entries[id] = &nEntry{
		parent: parent, src: src, mapped: mapped,
		isDir: isDir, pathLen: pl,
	}
	p.kids = append(p.kids, id)
	n.dirs[parent][src] = id
	n.keys[parent][nFold(mapped)] = id
	if isDir {
		n.dirs[id] = map[string]int64{}
		n.keys[id] = map[string]int64{}
	}
	return id, nil
}

func (n *naive) Remove(parent int64, src string) error {
	if !nValid(src) {
		return ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.isDir {
		return ErrNoParent
	}
	id, ok := n.dirs[parent][src]
	if !ok {
		return ErrNotFound
	}
	e := n.entries[id]
	if e.isDir && len(e.kids) > 0 {
		return ErrNotEmpty
	}
	ni := 0
	for i, k := range p.kids {
		if k == id {
			ni = i
		}
	}
	p.kids = append(p.kids[:ni], p.kids[ni+1:]...)
	delete(n.dirs[parent], src)
	delete(n.keys[parent], nFold(e.mapped))
	delete(n.entries, id)
	if e.isDir {
		delete(n.dirs, id)
		delete(n.keys, id)
	}
	return nil
}

func (n *naive) Rename(parent int64, src, newSrc string) error {
	if !nValid(newSrc) {
		return ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.isDir {
		return ErrNoParent
	}
	id, ok := n.dirs[parent][src]
	if !ok {
		return ErrNotFound
	}
	if newSrc == src {
		return nil
	}
	if _, ok := n.dirs[parent][newSrc]; ok {
		return ErrExists
	}
	e := n.entries[id]
	old := e.mapped
	delete(n.dirs[parent], src)
	delete(n.keys[parent], nFold(old))
	newM, err := n.allocate(parent, newSrc)
	if err != nil {
		n.dirs[parent][src] = id
		n.keys[parent][nFold(old)] = id
		return err
	}
	delta := len(newM) - len(old)
	if !n.subtreeOK(id, delta) {
		n.dirs[parent][src] = id
		n.keys[parent][nFold(old)] = id
		return ErrPathTooLong
	}
	n.bump(id, delta)
	e.src = newSrc
	e.mapped = newM
	n.dirs[parent][newSrc] = id
	n.keys[parent][nFold(newM)] = id
	return nil
}

func (n *naive) Lookup(parent int64, src string) (int64, bool, error) {
	if !nValid(src) {
		return 0, false, ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.isDir {
		return 0, false, ErrNoParent
	}
	id, ok := n.dirs[parent][src]
	if !ok {
		return 0, false, ErrNotFound
	}
	return id, n.entries[id].isDir, nil
}

func (n *naive) Path(id int64) (string, error) {
	e, ok := n.entries[id]
	if !ok {
		return "", ErrNotFound
	}
	var parts []string
	for id != 0 {
		parts = append(parts, e.mapped)
		id = e.parent
		e = n.entries[id]
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/"), nil
}

func (n *naive) Names(parent int64) ([]string, error) {
	p, ok := n.entries[parent]
	if !ok || !p.isDir {
		return nil, ErrNoParent
	}
	out := make([]string, 0, len(n.dirs[parent]))
	for _, id := range n.dirs[parent] {
		out = append(out, n.entries[id].mapped)
	}
	sort.Strings(out)
	return out, nil
}

func (n *naive) snapshot() []string {
	ids := make([]int64, 0, len(n.entries))
	for id := range n.entries {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		e := n.entries[id]
		p, _ := n.Path(id)
		out = append(out, fmt.Sprintf("%d:%s|%s|dir=%v|len=%d",
			id, e.src, p, e.isDir, e.pathLen))
	}
	return out
}
