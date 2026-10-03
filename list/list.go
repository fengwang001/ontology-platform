package list

import (
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"

	"ontology/authz"
	"ontology/keyindex"
)

// Request 是列举请求。
type Request struct {
	Prefix     string
	Delimiter  string
	StartAfter string
	Token      string
	Max        int
	Grants     []string
}

// Entry 是一条列举结果（普通键、键版本或公共前缀）。
type Entry struct {
	Key      string
	IsPrefix bool
	Version  int
	Deleted  bool
}

// Result 是一页列举结果。
type Result struct {
	Entries     []Entry
	IsTruncated bool
	NextToken   string
}

var (
	// ErrInvalidArgument 参数非法。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrInvalidToken 令牌缺失、被篡改或与请求不符。
	ErrInvalidToken = errors.New("invalid token")
)

type listKind uint8

const (
	kindObjects  listKind = 1
	kindVersions listKind = 2
)

// ListObjects 列举当前版本为数据版本的可见键。
func ListObjects(ix *keyindex.Index, req Request) (*Result, error) {
	return doList(ix, req, kindObjects)
}

// ListVersions 列举可见键的全部版本。
func ListVersions(ix *keyindex.Index, req Request) (*Result, error) {
	return doList(ix, req, kindVersions)
}

func doList(ix *keyindex.Index, req Request, kind listKind) (*Result, error) {
	if req.Max < 1 || req.Max > 1000 {
		return nil, ErrInvalidArgument
	}
	if len(req.Delimiter) > 1 {
		return nil, ErrInvalidArgument
	}
	if req.StartAfter != "" && req.Token != "" {
		return nil, ErrInvalidArgument
	}

	var anchor string
	var resumeVer int
	if req.Token != "" {
		var err error
		anchor, resumeVer, err = decodeToken(req.Token, kind, req.Prefix, req.Delimiter)
		if err != nil {
			return nil, err
		}
	} else {
		anchor = req.StartAfter
	}

	cur := newCursor(ix, req, kind)
	if anchor != "" {
		start := anchor
		if req.Prefix > start {
			start = req.Prefix
		}
		cur.snap.SeekGE(start)
		if kind == kindObjects {
			if k, ok := cur.snap.Current(); ok && k == anchor && anchor >= req.Prefix {
				cur.snap.Next()
			}
		} else {
			if anchor < req.Prefix {
				anchor = ""
			}
			cur.anchorKey = anchor
			cur.resumeVer = resumeVer
			if resumeVer == 0 {
				// StartAfter：精确命中锚点键时先排除该键（分组条目同样不得由它产生）。
				if k, ok := cur.snap.Current(); ok && k == anchor {
					cur.snap.Next()
				}
			}
		}
	} else {
		cur.snap.SeekGE(req.Prefix)
	}

	res := &Result{Entries: make([]Entry, 0, req.Max)}
	for len(res.Entries) < req.Max {
		e, more := cur.next()
		if !more {
			break
		}
		res.Entries = append(res.Entries, e)
	}

	if len(res.Entries) == req.Max {
		if _, more := cur.next(); more {
			res.IsTruncated = true
			res.NextToken = encodeToken(kind, req.Prefix, req.Delimiter, res.Entries[len(res.Entries)-1])
		}
	}
	return res, nil
}

type cursor struct {
	snap           *keyindex.Snapshot
	prefix         string
	delim          string
	grants         []string
	gi             int
	kind           listKind
	anchorKey      string
	resumeVer      int
	versions       []keyindex.Version
	vi             int
	versionKey     string
	versionHeld    bool
	markersSkipped int
}

func newCursor(ix *keyindex.Index, req Request, kind listKind) *cursor {
	c := &cursor{
		snap:   ix.Snapshot(),
		prefix: req.Prefix,
		delim:  req.Delimiter,
		grants: authz.Normalize(req.Grants),
		kind:   kind,
	}
	if len(c.grants) > 0 && c.grants[0] == "" {
		c.gi = 0
	} else {
		c.gi = c.firstGrant()
	}
	return c
}

func (c *cursor) prefixEnd() string {
	if e, ok := keyindex.Succ(c.prefix); ok {
		return e
	}
	return ""
}

// firstGrant 返回首个可能与 [prefix, succ(prefix)) 相交的授权前缀下标。
func (c *cursor) firstGrant() int {
	hi := c.prefixEnd()
	for i, g := range c.grants {
		if hi != "" && g >= hi {
			return len(c.grants)
		}
		if g >= c.prefix || strings.HasPrefix(c.prefix, g) {
			return i
		}
	}
	return len(c.grants)
}

func (c *cursor) pastPrefix(k string) bool {
	end := c.prefixEnd()
	return end != "" && k >= end
}

// visible 判断键 k 是否可见，并把授权游标推进到与当前判断一致的位置。
func (c *cursor) visible(k string) bool {
	for c.gi < len(c.grants) {
		g := c.grants[c.gi]
		if g == "" || strings.HasPrefix(k, g) {
			return true
		}
		if g > k || c.pastPrefix(g) {
			return false
		}
		c.gi++
	}
	return false
}

// jumpHidden 从不可见键一次寻址跳到下一个授权前缀起点；前缀已过完则到末尾。
func (c *cursor) jumpHidden(curKey string) {
	if c.gi < len(c.grants) && c.grants[c.gi] == "" {
		return
	}
	for c.gi < len(c.grants) {
		g := c.grants[c.gi]
		if g > curKey && !c.pastPrefix(g) {
			c.snap.SeekGE(g)
			return
		}
		c.gi++
	}
	c.snap.End()
}

// next 返回下一个条目；收满一页后再调一次用于判定截断。
func (c *cursor) next() (Entry, bool) {
	for {
		if c.kind == kindVersions && c.versionHeld {
			if e, ok := c.emitVersion(); ok {
				return e, true
			}
			continue
		}

		k, ok := c.snap.Current()
		if !ok || c.pastPrefix(k) {
			return Entry{}, false
		}

		if !c.visible(k) {
			c.jumpHidden(k)
			continue
		}

		if c.delim != "" {
			if i := strings.Index(k[len(c.prefix):], c.delim); i >= 0 {
				p := c.prefix + k[len(c.prefix):len(c.prefix)+i+1]
				if s, ok := keyindex.Succ(p); ok {
					c.snap.SeekGE(s)
				} else {
					c.snap.End()
				}
				return Entry{Key: p, IsPrefix: true}, true
			}
		}

		vers := c.snap.Versions(k)
		if c.kind == kindObjects {
			c.snap.Next()
			if len(vers) == 0 || vers[0].Deleted {
				c.markersSkipped++
				continue
			}
			return Entry{Key: k}, true
		}

		c.versionKey = k
		c.versions = vers
		c.vi = 0
		c.versionHeld = true
	}
}

// emitVersion 在版本列举中发出同键的后续版本，并处理锚点（StartAfter/令牌）续接。
func (c *cursor) emitVersion() (Entry, bool) {
	for {
		if c.vi >= len(c.versions) {
			c.versions = nil
			c.versionHeld = false
			c.snap.Next()
			return Entry{}, false
		}
		v := c.versions[c.vi]
		c.vi++
		if c.anchorKey != "" {
			switch {
			case c.versionKey < c.anchorKey:
				c.versions = nil
				c.versionHeld = false
				c.snap.Next()
				return Entry{}, false
			case c.versionKey == c.anchorKey:
				if c.resumeVer != 0 {
					if v.Number >= c.resumeVer {
						continue
					}
					c.anchorKey, c.resumeVer = "", 0
				} else {
					// StartAfter：排除该锚点键的全部版本。
					c.versions = nil
					c.versionHeld = false
					c.snap.Next()
					return Entry{}, false
				}
			default:
				c.anchorKey, c.resumeVer = "", 0
			}
		}
		if c.vi >= len(c.versions) {
			c.versions = nil
			c.versionHeld = false
			c.snap.Next()
		}
		return Entry{Key: c.versionKey, Version: v.Number, Deleted: v.Deleted}, true
	}
}

// ---- 令牌 ----

func encodeToken(kind listKind, prefix, delim string, last Entry) string {
	tag := "k"
	marker := encodeKey(last.Key)
	switch {
	case last.IsPrefix:
		tag = "p"
		// 键空间末尾不会再产生续页；此分支仅在 succ 存在时被调用。
		s, _ := keyindex.Succ(last.Key)
		marker = encodeKey(s)
	case kind == kindVersions:
		tag = "v"
		marker = encodeKey(last.Key)
	}
	head := strconv.Itoa(int(kind))
	verField := ""
	if tag == "v" {
		verField = strconv.Itoa(last.Version)
	}
	fields := strings.Join([]string{head, esc(prefix), esc(delim), tag, marker, verField}, "\n")
	crc := crc32.ChecksumIEEE([]byte(fields))
	return fields + "\n" + fmt.Sprintf("%08x", crc)
}

func decodeToken(tok string, kind listKind, prefix, delim string) (string, int, error) {
	idx := strings.LastIndexByte(tok, '\n')
	if idx < 0 || len(tok) != idx+9 {
		return "", 0, ErrInvalidToken
	}
	body := tok[:idx]
	got, err := strconv.ParseUint(tok[idx+1:], 16, 32)
	if err != nil || crc32.ChecksumIEEE([]byte(body)) != uint32(got) {
		return "", 0, ErrInvalidToken
	}

	parts := strings.SplitN(body, "\n", 6)
	if len(parts) != 6 || len(parts[0]) < 1 {
		return "", 0, ErrInvalidToken
	}
	if parts[0] != strconv.Itoa(int(kind)) {
		return "", 0, ErrInvalidToken
	}
	if unesc(parts[1]) != prefix || unesc(parts[2]) != delim {
		return "", 0, ErrInvalidToken
	}
	tag, marker := parts[3], parts[4]
	switch tag {
	case "k":
		if parts[5] != "" {
			return "", 0, ErrInvalidToken
		}
		return unesc(marker), 0, nil
	case "p":
		if marker == "" || parts[5] != "" {
			return "", 0, ErrInvalidToken
		}
		return unesc(marker), 0, nil
	case "v":
		if kind != kindVersions {
			return "", 0, ErrInvalidToken
		}
		vn, err := strconv.Atoi(parts[5])
		if err != nil || vn <= 0 {
			return "", 0, ErrInvalidToken
		}
		return unesc(marker), vn, nil
	default:
		return "", 0, ErrInvalidToken
	}
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func unesc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == 'n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// encodeKey 与 esc 相同，键内换行需转义。
func encodeKey(s string) string { return esc(s) }
