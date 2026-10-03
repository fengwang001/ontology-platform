// Package list 提供带分隔符汇总、版本列举与授权过滤的分页列举服务。
package list

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/authz"
	"ontology/keyindex"
)

// Kind 标识列举种类。
type Kind uint8

const (
	Objects  Kind = 1
	Versions Kind = 2
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrInvalidToken    = errors.New("invalid token")
)

// Request 是一次列举请求。
type Request struct {
	Kind       Kind
	Prefix     string
	Delimiter  string
	StartAfter string
	Token      string
	Max        int
	Grants     []string
}

// Entry 是一个结果条目：对象键、公共前缀或某个版本。
type Entry struct {
	IsPrefix bool   // 公共前缀条目
	Key      string // 对象键或公共前缀
	Version  int64  // Versions 下的版本号；Objects 为 0
	IsDelete bool   // 版本是否为删除标记
}

// Response 是一页列举结果。
type Response struct {
	Entries     []Entry
	IsTruncated bool
	NextToken   string
}

// Server 持有键索引；所有方法可并发调用。
type Server struct {
	idx *keyindex.Index
}

// New 创建列举服务。
func New(idx *keyindex.Index) *Server { return &Server{idx: idx} }

// ListObjects 只列举当前版本为数据版本的可见键。
func (s *Server) ListObjects(req Request) (Response, error) {
	req.Kind = Objects
	return s.doList(req)
}

// ListVersions 列举可见键的全部版本（含删除标记）。
func (s *Server) ListVersions(req Request) (Response, error) {
	req.Kind = Versions
	return s.doList(req)
}

func (s *Server) doList(req Request) (Response, error) {
	// 参数非法优先于令牌校验。
	if req.Max < 1 || req.Max > 1000 {
		return Response{}, ErrInvalidArgument
	}
	if len(req.Delimiter) > 1 {
		return Response{}, ErrInvalidArgument
	}
	if req.StartAfter != "" && req.Token != "" {
		return Response{}, ErrInvalidArgument
	}

	sc := &scanner{
		snap:   s.idx.Snapshot(), // 一页基于同一时刻的索引
		grants: authz.Normalize(req.Grants),
		prefix: req.Prefix,
		delim:  req.Delimiter,
		kind:   req.Kind,
		seek:   req.Prefix,
	}

	if req.Token != "" {
		tok, err := decodeToken(req.Token)
		if err != nil {
			return Response{}, ErrInvalidToken
		}
		if tok.kind != req.Kind || tok.prefix != req.Prefix || tok.delim != req.Delimiter {
			return Response{}, ErrInvalidToken
		}
		sc.seek = tok.resume
		sc.resumeVer = tok.resumeVer
	} else if req.StartAfter != "" {
		sc.seek = req.StartAfter
		if req.StartAfter < req.Prefix {
			sc.seek = req.Prefix
		}
		sc.strict = req.StartAfter
	}

	resp := Response{Entries: make([]Entry, 0, req.Max)}
	for len(resp.Entries) < req.Max {
		e, ok := sc.next()
		if !ok {
			break
		}
		resp.Entries = append(resp.Entries, e)
	}

	// 多看一个条目以决定截断；公共前缀不会被逐键扫开。
	if peek, ok := sc.next(); ok {
		_ = peek
		resp.IsTruncated = true
		last := resp.Entries[len(resp.Entries)-1]
		resp.NextToken = encodeToken(req.Kind, req.Prefix, req.Delimiter, last)
	}
	return resp, nil
}

// scanner 在单个不可变快照上做游标式扫描。
type scanner struct {
	snap      *keyindex.Snapshot
	grants    authz.Grants
	prefix    string
	delim     string
	kind      Kind
	seek      string // 首次 SeekGE 键
	strict    string // 只保留严格大于该键的键（StartAfter）
	resumeVer int64  // Versions 续页：首个等于 seek 的键只留更小版本

	started  bool
	accepted bool // 当前键已通过 Prefix/起点/授权过滤
	pos      int
	key      string
	versions []keyindex.Version
	verIdx   int // 当前键下一次待返回版本下标
	eof      bool
}

func (c *scanner) seekTo(k string) bool {
	key, versions, pos, ok := c.snap.SeekGE(k)
	if !ok {
		c.eof = true
		c.key = ""
		c.accepted = false
		return false
	}
	c.pos, c.key, c.versions, c.verIdx = pos, key, versions, 0
	c.accepted = false
	return true
}

// advanceKey 位置寻址前进到下一物理键，不产生 seek。
func (c *scanner) advanceKey() bool {
	key, versions, ok := c.snap.At(c.pos + 1)
	if !ok {
		c.eof = true
		c.key = ""
		c.accepted = false
		return false
	}
	c.pos++
	c.key, c.versions, c.verIdx = key, versions, 0
	c.accepted = false
	return true
}

func (c *scanner) next() (Entry, bool) {
	if !c.started {
		c.started = true
		if c.seek == "" {
			if !c.seekTo("") {
				return Entry{}, false
			}
		} else if !c.seekTo(c.seek) {
			return Entry{}, false
		}
	}

	for !c.eof {
		// Versions：当前键已通过过滤且尚有版本时优先吐出。
		if c.kind == Versions && c.accepted && c.verIdx < len(c.versions) {
			v := c.versions[c.verIdx]
			c.verIdx++
			return Entry{Key: c.key, Version: v.ID, IsDelete: v.IsDelete}, true
		}
		if c.kind == Versions && c.accepted && c.verIdx >= len(c.versions) {
			if !c.advanceKey() {
				return Entry{}, false
			}
			continue
		}

		key := c.key
		c.accepted = false

		// 定位下一个通过全部过滤的物理键。
		for {
			if key < c.prefix {
				// 尚在 Prefix 之前：直接寻址进 Prefix 区间。
				if !c.seekTo(c.prefix) {
					return Entry{}, false
				}
				key = c.key
				continue
			}
			if !hasStrPrefix(key, c.prefix) {
				// 已越过 Prefix 键空间：结束。
				c.eof = true
				return Entry{}, false
			}
			if c.strict != "" && key <= c.strict {
				if !c.advanceKey() {
					return Entry{}, false
				}
				key = c.key
				continue
			}
			if !c.grants.Visible(key) {
				// 不可见区间整体跳到下一授权前缀，不逐键扫描。
				nk, visible := c.grants.NextVisible(key)
				if !visible || !c.seekTo(nk) {
					c.eof = true
					return Entry{}, false
				}
				key = c.key
				continue
			}
			break
		}

		rest := key[len(c.prefix):]
		if c.delim != "" {
			if i := indexByte(rest, c.delim[0]); i >= 0 {
				group := c.prefix + rest[:i+1]
				// 公共前缀只由可见键产生；整组以 succ 寻址跳过，
				// succ 之后无键也要照常产出本组（seekTo 已置 eof）。
				if succ, ok := keyindex.Succ(group); ok {
					c.seekTo(succ)
				} else {
					c.eof = true
				}
				return Entry{IsPrefix: true, Key: group}, true
			}
		}

		if c.kind == Objects {
			if len(c.versions) > 0 && c.versions[0].IsDelete {
				if !c.advanceKey() {
					return Entry{}, false
				}
				continue
			}
			c.advanceKey()
			return Entry{Key: key}, true
		}

		// Versions：续页同键时丢弃已返回版本。
		if c.resumeVer > 0 && key == c.seek {
			for c.verIdx < len(c.versions) && c.versions[c.verIdx].ID >= c.resumeVer {
				c.verIdx++
			}
			c.resumeVer = 0
			if c.verIdx == len(c.versions) {
				if !c.advanceKey() {
					return Entry{}, false
				}
				continue
			}
		}
		c.accepted = true
		v := c.versions[c.verIdx]
		c.verIdx++
		return Entry{Key: key, Version: v.ID, IsDelete: v.IsDelete}, true
	}
	return Entry{}, false
}

func hasStrPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// --- 令牌：版本化二进制 body + IEEE CRC32，base64url 封装 ---

type tokenData struct {
	kind      Kind
	prefix    string
	delim     string
	resume    string
	resumeVer int64
}

func putBytes(b []byte, s string) []byte {
	var ln [4]byte
	binary.BigEndian.PutUint32(ln[:], uint32(len(s)))
	b = append(b, ln[:]...)
	return append(b, s...)
}

func getBytes(b []byte) (string, []byte, bool) {
	if len(b) < 4 {
		return "", nil, false
	}
	n := int(binary.BigEndian.Uint32(b[:4]))
	b = b[4:]
	if len(b) < n {
		return "", nil, false
	}
	return string(b[:n]), b[n:], true
}

func encodeToken(kind Kind, prefix, delim string, last Entry) string {
	var resume string
	var resumeVer int64
	switch {
	case last.IsPrefix:
		// 公共前缀：整组跳过，从 succ(P) 续。
		if succ, ok := keyindex.Succ(last.Key); ok {
			resume = succ
		} else {
			resume = "\xff\xff\xff\xff" // 键空间末尾，恢复后无条目
		}
	case kind == Versions:
		// 版本：从同键更小版本续；resumeVer 排除已返回版本。
		resume = last.Key
		resumeVer = last.Version
	default:
		// 普通对象键：严格之后的最小键 K+"\x00"。
		resume = last.Key + "\x00"
	}

	body := []byte{byte(kind), 1}
	body = putBytes(body, prefix)
	body = putBytes(body, delim)
	body = putBytes(body, resume)
	var ver [8]byte
	binary.BigEndian.PutUint64(ver[:], uint64(resumeVer))
	body = append(body, ver[:]...)

	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(body))
	return base64.RawURLEncoding.EncodeToString(append(body, sum[:]...))
}

func decodeToken(s string) (tokenData, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < 2+8+4 {
		return tokenData{}, ErrInvalidToken
	}
	body, sum := raw[:len(raw)-4], raw[len(raw)-4:]
	if binary.BigEndian.Uint32(sum) != crc32.ChecksumIEEE(body) {
		return tokenData{}, ErrInvalidToken
	}
	if body[1] != 1 {
		return tokenData{}, ErrInvalidToken
	}

	t := tokenData{kind: Kind(body[0])}
	b := body[2:]
	var ok bool
	if t.prefix, b, ok = getBytes(b); !ok {
		return tokenData{}, ErrInvalidToken
	}
	if t.delim, b, ok = getBytes(b); !ok {
		return tokenData{}, ErrInvalidToken
	}
	if t.resume, b, ok = getBytes(b); !ok {
		return tokenData{}, ErrInvalidToken
	}
	if len(b) < 8 {
		return tokenData{}, ErrInvalidToken
	}
	t.resumeVer = int64(binary.BigEndian.Uint64(b[:8]))
	return t, nil
}
