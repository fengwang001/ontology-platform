package segment

import (
	"encoding/binary"
	"hash/crc32"

	"ontology/posting"
)

// Reader 是一个段的只读视图（可能来自恢复，仅含完整词）。
type Reader struct {
	raw        []byte
	postsStart int
	dataEnd    int
	entries    []Entry
	index      map[string]int
}

func parseDict(raw []byte, start, end, limit int) ([]Entry, bool) {
	off := start
	tv, no, err := posting.ReadUvarint(raw, off)
	if err != nil || no > end || no > limit {
		return nil, false
	}
	off = no
	termNum := int(tv)
	entries := make([]Entry, 0, termNum)
	bound := end
	if limit < bound {
		bound = limit
	}
	for i := 0; i < termNum; i++ {
		tl, o1, e := posting.ReadUvarint(raw, off)
		if e != nil || o1 > bound {
			return nil, false
		}
		off = o1
		if int(tl)+off > bound {
			return nil, false
		}
		term := string(raw[off : off+int(tl)])
		off += int(tl)
		po, o2, e := posting.ReadUvarint(raw, off)
		if e != nil || o2 > bound {
			return nil, false
		}
		off = o2
		pl, o3, e := posting.ReadUvarint(raw, off)
		if e != nil || o3+4 > bound {
			return nil, false
		}
		off = o3
		cc := binary.LittleEndian.Uint32(raw[off : off+4])
		off += 4
		entries = append(entries, Entry{Term: term, PostOff: po, PostLen: pl, ChainCRC: cc})
	}
	return entries, true
}

// Open 严格打开完整段，任何问题返回四类错误之一（可用 errors.Is）。
func Open(raw []byte) (*Reader, error) {
	if len(raw) < 12 || string(raw[:min(8, len(raw))]) != Magic[:min(8, len(raw))] {
		return nil, ErrHeader
	}
	dataLen := int(binary.LittleEndian.Uint32(raw[8:12]))
	dl, dlo, err := posting.ReadUvarint(raw, 12)
	if err != nil || 12+dataLen < dlo+int(dl) {
		return nil, ErrDict
	}
	dictStart := dlo
	dictEnd := dictStart + int(dl)
	if len(raw) < dictEnd {
		return nil, ErrDict
	}
	entries, ok := parseDict(raw, dictStart, dictEnd, len(raw))
	if !ok {
		return nil, ErrDict
	}
	dataEnd := 12 + dataLen
	if len(raw) < dataEnd {
		return nil, ErrPosting
	}
	if len(raw) < dataEnd+4 {
		return nil, ErrCRC
	}
	want := binary.LittleEndian.Uint32(raw[dataEnd : dataEnd+4])
	if crc32.Checksum(raw[:dataEnd], crcTable) != want {
		return nil, ErrCRC
	}
	r := &Reader{raw: raw, postsStart: dictEnd, dataEnd: dataEnd, entries: entries}
	for _, e := range entries {
		if e.PostOff+e.PostLen > uint64(dataEnd-dictEnd) {
			return nil, ErrPosting
		}
		sl := raw[dictEnd+int(e.PostOff) : dictEnd+int(e.PostOff)+int(e.PostLen)]
		if crc32.Checksum(sl, crcTable) != e.ChainCRC {
			return nil, ErrPosting
		}
	}
	r.index = map[string]int{}
	for i, e := range entries {
		r.index[e.Term] = i
	}
	return r, nil
}

// Recover 返回最大可恢复前缀：仅保留倒排链完整且链 CRC 通过的词。
// 即使返回非 nil 的错误（原始段不完整），也可能同时返回可用 Reader。
func Recover(raw []byte) (*Reader, error) {
	openErr := ErrHeader
	if len(raw) < 12 || string(raw[:min(8, len(raw))]) != Magic[:min(8, len(raw))] {
		return nil, openErr
	}
	dataLen := int(binary.LittleEndian.Uint32(raw[8:12]))
	dl, dlo, err := posting.ReadUvarint(raw, 12)
	if err != nil {
		return &Reader{index: map[string]int{}}, ErrDict
	}
	dictStart := dlo
	declaredDictEnd := dictStart + int(dl)
	r := &Reader{raw: raw, postsStart: declaredDictEnd, dataEnd: 12 + dataLen, index: map[string]int{}}
	if len(raw) < declaredDictEnd {
		return r, ErrDict
	}
	entries, ok := parseDict(raw, dictStart, declaredDictEnd, len(raw))
	if !ok {
		return r, ErrDict
	}
	availablePosts := len(raw) - declaredDictEnd
	if availablePosts < 0 {
		availablePosts = 0
	}
	if len(raw) >= r.dataEnd+4 &&
		crc32.Checksum(raw[:r.dataEnd], crcTable) ==
			binary.LittleEndian.Uint32(raw[r.dataEnd:r.dataEnd+4]) {
		openErr = nil
		availablePosts = r.dataEnd - declaredDictEnd
	} else if len(raw) < r.dataEnd {
		openErr = ErrPosting
	} else {
		openErr = ErrCRC
		availablePosts = r.dataEnd - declaredDictEnd
	}
	for _, e := range entries {
		if e.PostOff+e.PostLen > uint64(availablePosts) {
			break
		}
		sl := raw[declaredDictEnd+int(e.PostOff) : declaredDictEnd+int(e.PostOff)+int(e.PostLen)]
		if crc32.Checksum(sl, crcTable) != e.ChainCRC {
			break
		}
		if _, derr := posting.Decode(sl); derr != nil {
			break
		}
		r.index[e.Term] = len(r.entries)
		r.entries = append(r.entries, e)
	}
	return r, openErr
}

// Terms 返回恢复后可读词元（字典序）。
func (r *Reader) Terms() []string {
	out := make([]string, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.Term
	}
	return out
}

// Chain 读取某词的倒排链。
func (r *Reader) Chain(term string) (*posting.Chain, bool) {
	i, ok := r.index[term]
	if !ok {
		return nil, false
	}
	e := r.entries[i]
	start := r.postsStart + int(e.PostOff)
	chain, err := posting.Decode(r.raw[start : start+int(e.PostLen)])
	if err != nil {
		return nil, false
	}
	chain.Term = term
	return chain, true
}
