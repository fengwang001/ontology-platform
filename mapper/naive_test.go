package mapper

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"unicode/utf8"
)

// naiveMapper is an independent, step-by-step transcription of the spec,
// used purely as a differential oracle. It shares no helper with the
// production implementation.
type naiveEntry struct {
	id     int
	parent int
	src    string
	mapped string
	dir    bool
	kids   map[string]*naiveEntry
}

type naiveMapper struct {
	maxBytes, maxPath, maxEntries int
	nextID                        int
	entries                       map[int]*naiveEntry
}

func newNaive(cfg Config) *naiveMapper {
	root := &naiveEntry{id: 0, parent: -1, dir: true, kids: map[string]*naiveEntry{}}
	return &naiveMapper{
		maxBytes: cfg.MaxBytes, maxPath: cfg.MaxPath, maxEntries: cfg.MaxEntries,
		nextID: 1, entries: map[int]*naiveEntry{0: root},
	}
}

func naiveValidName(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsRune(s, '/') || strings.ContainsRune(s, 0) {
		return false
	}
	return utf8.ValidString(s)
}

func naiveEsc(b byte) string {
	return fmt.Sprintf("%%%02X", b)
}

func naiveStep1(src string) string {
	var out strings.Builder
	for _, r := range src {
		if r <= 0x7F {
			c := byte(r)
			switch {
			case strings.IndexByte("<>?:\"\\|*%", c) >= 0, c < 0x20:
				out.WriteString(naiveEsc(c))
			default:
				out.WriteByte(c)
			}
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func naiveFoldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func naiveReserved(stem string) bool {
	switch stem {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(stem) == 4 {
		p := stem[:3]
		d := stem[3]
		if (p == "com" || p == "lpt") && d >= '1' && d <= '9' {
			return true
		}
	}
	return false
}

func naiveTokenize(s string) []string {
	var toks []string
	for len(s) > 0 {
		if s[0] == '%' && len(s) >= 3 && naiveIsHex(s[1]) && naiveIsHex(s[2]) {
			toks = append(toks, s[:3])
			s = s[3:]
		} else {
			_, sz := utf8.DecodeRuneInString(s)
			toks = append(toks, s[:sz])
			s = s[sz:]
		}
	}
	return toks
}

func naiveIsHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func naiveBase(src string, maxBytes int) string {
	s := naiveStep1(src)
	if strings.HasSuffix(s, ".") {
		s = s[:len(s)-1] + "%2E"
	} else if strings.HasSuffix(s, " ") {
		s = s[:len(s)-1] + "%20"
	}
	stem := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		stem = s[:i]
	}
	if naiveReserved(naiveFoldASCII(stem)) {
		_, sz := utf8.DecodeRuneInString(s)
		s = naiveEsc(s[0]) + s[sz:]
	}
	if len(s) > maxBytes {
		h := fnv.New32a()
		h.Write([]byte(s))
		tail := fmt.Sprintf("~%08X", h.Sum32())
		toks := naiveTokenize(s)
		prefix := ""
		for _, tk := range toks {
			if len(prefix)+len(tk)+len(tail) <= maxBytes {
				prefix += tk
			} else {
				break
			}
		}
		s = prefix + tail
	}
	return s
}

func naiveSplit(t0 string) (base, ext string) {
	i := strings.LastIndexByte(t0, '.')
	if i > 0 {
		return t0[:i], t0[i:]
	}
	return t0, ""
}

func (n *naiveMapper) dirUsed(d *naiveEntry) map[string]string {
	used := map[string]string{}
	for _, e := range d.kids {
		used[naiveFoldASCII(e.mapped)] = e.mapped
	}
	return used
}

func naiveAlloc(d *naiveEntry, t0 string, maxBytes int) (string, error) {
	used := map[string]bool{}
	for _, e := range d.kids {
		used[naiveFoldASCII(e.mapped)] = true
	}
	if !used[naiveFoldASCII(t0)] {
		return t0, nil
	}
	base0, ext := naiveSplit(t0)
	toks := naiveTokenize(base0)
	for num := 2; ; num++ {
		suffix := "~" + fmt.Sprintf("%d", num)
		cut := append([]string{}, toks...)
		for {
			base := strings.Join(cut, "")
			cand := base + suffix + ext
			if len(cand) <= maxBytes {
				if !used[naiveFoldASCII(cand)] {
					return cand, nil
				}
				break
			}
			if len(cut) == 0 {
				return "", ErrCannotFit
			}
			cut = cut[:len(cut)-1]
		}
	}
}

func (n *naiveMapper) pathLen(e *naiveEntry) int {
	total, cur := -1, e
	for cur.id != 0 {
		total += len(cur.mapped) + 1
		cur = n.entries[cur.parent]
	}
	return total
}

func (n *naiveMapper) subtreeOK(e *naiveEntry) bool {
	if n.pathLen(e) > n.maxPath {
		return false
	}
	if e.dir {
		for _, c := range e.kids {
			if !n.subtreeOK(c) {
				return false
			}
		}
	}
	return true
}

func (n *naiveMapper) add(parent int, src string, dir bool) (int, error) {
	if !naiveValidName(src) {
		return 0, ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.dir {
		return 0, ErrNoParent
	}
	if _, ok := p.kids[src]; ok {
		return 0, ErrExists
	}
	if len(p.kids) >= n.maxEntries {
		return 0, ErrFull
	}
	name, err := naiveAlloc(p, naiveBase(src, n.maxBytes), n.maxBytes)
	if err != nil {
		return 0, err
	}
	e := &naiveEntry{id: n.nextID, parent: parent, src: src, mapped: name, dir: dir}
	if dir {
		e.kids = map[string]*naiveEntry{}
	}
	if n.pathLen(e) > n.maxPath {
		return 0, ErrPathTooLong
	}
	p.kids[src] = e
	n.entries[e.id] = e
	n.nextID++
	return e.id, nil
}

func (n *naiveMapper) remove(parent int, src string) error {
	p, ok := n.entries[parent]
	if !ok || !p.dir {
		return ErrNotFound
	}
	e, ok := p.kids[src]
	if !ok {
		return ErrNotFound
	}
	if e.dir && len(e.kids) > 0 {
		return ErrNotEmpty
	}
	delete(p.kids, src)
	delete(n.entries, e.id)
	return nil
}

func (n *naiveMapper) rename(parent int, src, newSrc string) error {
	if !naiveValidName(newSrc) {
		return ErrInvalidName
	}
	p, ok := n.entries[parent]
	if !ok || !p.dir {
		return ErrNotFound
	}
	e, ok := p.kids[src]
	if !ok {
		return ErrNotFound
	}
	if newSrc == src {
		return nil
	}
	if _, ok := p.kids[newSrc]; ok {
		return ErrExists
	}
	old := e.mapped
	delete(p.kids, src)
	name, err := naiveAlloc(p, naiveBase(newSrc, n.maxBytes), n.maxBytes)
	if err != nil {
		p.kids[src] = e
		e.mapped = old
		return err
	}
	e.src, e.mapped = newSrc, name
	if !n.subtreeOK(e) {
		e.src, e.mapped = src, old
		p.kids[src] = e
		return ErrPathTooLong
	}
	p.kids[newSrc] = e
	return nil
}

func (n *naiveMapper) snapshot() map[string]string {
	out := map[string]string{}
	var walk func(d *naiveEntry, prefix string)
	walk = func(d *naiveEntry, prefix string) {
		names := make([]string, 0, len(d.kids))
		for _, e := range d.kids {
			names = append(names, e.mapped)
		}
		sort.Strings(names)
		for _, nm := range names {
			_ = nm
		}
		for _, e := range d.kids {
			full := nmJoin(prefix, e.mapped)
			out[full] = kindOf(e)
			if e.dir {
				walk(e, full)
			}
		}
	}
	walk(n.entries[0], "")
	return out
}

func kindOf(e *naiveEntry) string {
	if e.dir {
		return "d"
	}
	return "f"
}

func nmJoin(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}
