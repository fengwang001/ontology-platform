// Package segment 定义段文件格式（自描述头 + 词典 + 倒排链 + CRC32）的写读。
package segment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"sort"

	"ontology/posting"
)

// 四类截断/损坏错误，可用 errors.Is 区分。
var (
	ErrHeaderIncomplete   = errors.New("segment: header incomplete")
	ErrDictIncomplete     = errors.New("segment: dictionary incomplete")
	ErrPostingsIncomplete = errors.New("segment: postings incomplete")
	ErrCRCMismatch        = errors.New("segment: crc mismatch")
)

var magic = []byte("OSG1")

const (
	version    = 1
	headerSize = 16 // magic[4] + version u32 + termCount u32 + dictLen u32
	crcSize    = 4
)

// Segment 是内存中的段：词典有序，倒排链完整。
type Segment struct {
	Terms []string
	Lists map[string]posting.List
}

// New 由词->倒排链构建段，词典按词排序。
func New(lists map[string]posting.List) *Segment {
	s := &Segment{Lists: lists}
	for t := range lists {
		s.Terms = append(s.Terms, t)
	}
	sort.Strings(s.Terms)
	return s
}

// Lookup 返回词的倒排链。
func (s *Segment) Lookup(term string) (posting.List, bool) {
	l, ok := s.Lists[term]
	return l, ok
}

// encode 序列化为完整文件字节（含 CRC）。
func (s *Segment) encode() []byte {
	var dict, posts bytes.Buffer
	for _, t := range s.Terms {
		enc := posting.Encode(s.Lists[t])
		binary.Write(&dict, binary.LittleEndian, uint32(len(t)))
		dict.WriteString(t)
		binary.Write(&dict, binary.LittleEndian, uint32(posts.Len()))
		binary.Write(&dict, binary.LittleEndian, uint32(len(enc)))
		posts.Write(enc)
	}
	var buf bytes.Buffer
	buf.Write(magic)
	binary.Write(&buf, binary.LittleEndian, uint32(version))
	binary.Write(&buf, binary.LittleEndian, uint32(len(s.Terms)))
	binary.Write(&buf, binary.LittleEndian, uint32(dict.Len()))
	buf.Write(dict.Bytes())
	buf.Write(posts.Bytes())
	binary.Write(&buf, binary.LittleEndian, crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes()
}

// Write 将段写入 path。
func Write(path string, s *Segment) error {
	return os.WriteFile(path, s.encode(), 0o644)
}

// Read 严格读取段文件；任何截断/损坏返回四类错误之一。
func Read(path string) (*Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data, false)
}

// ReadRecover 读取最大可恢复前缀：词典中倒排链不完整的词被剔除，
// 返回自洽的段与分类错误（文件完好时错误为 nil）。
func ReadRecover(path string) (*Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data, true)
}

func u32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func parse(data []byte, recoverMode bool) (*Segment, error) {
	if len(data) < headerSize || !bytes.Equal(data[:4], magic) {
		return nil, ErrHeaderIncomplete
	}
	if u32(data[4:8]) != version {
		return nil, fmt.Errorf("%w: bad version", ErrHeaderIncomplete)
	}
	termCount, dictLen := u32(data[8:12]), u32(data[12:16])
	dictEnd := headerSize + int(dictLen)
	if len(data) < dictEnd {
		return nil, ErrDictIncomplete
	}
	type entry struct {
		term        string
		off, length uint32
	}
	dict := data[headerSize:dictEnd]
	entries := make([]entry, 0, termCount)
	for off := 0; off < len(dict); {
		if len(dict)-off < 4 {
			return nil, ErrDictIncomplete
		}
		tl := int(u32(dict[off:]))
		off += 4
		if len(dict)-off < tl+8 {
			return nil, ErrDictIncomplete
		}
		term := string(dict[off : off+tl])
		e := entry{term, u32(dict[off+tl:]), u32(dict[off+tl+4:])}
		entries = append(entries, e)
		off += tl + 8
	}
	if uint32(len(entries)) != termCount {
		return nil, ErrDictIncomplete
	}
	var postingsLen uint32
	for _, e := range entries {
		if e.off+e.length > postingsLen {
			postingsLen = e.off + e.length
		}
	}
	full := dictEnd + int(postingsLen) + crcSize
	classify := func() error {
		if len(data) < dictEnd+int(postingsLen) {
			return ErrPostingsIncomplete
		}
		return ErrCRCMismatch
	}
	truncated := len(data) < full
	if !truncated && crc32.ChecksumIEEE(data[:len(data)-crcSize]) != u32(data[len(data)-crcSize:]) {
		return nil, ErrCRCMismatch
	}
	if truncated && !recoverMode {
		return nil, classify()
	}
	avail := len(data) - dictEnd // 截断时末尾无 CRC，全部剩余字节皆倒排区
	if avail < 0 {
		avail = 0
	}
	if avail > int(postingsLen) {
		avail = int(postingsLen)
	}
	posts := data[dictEnd : dictEnd+avail]
	s := &Segment{Lists: map[string]posting.List{}}
	for _, e := range entries {
		if int(e.off+e.length) > len(posts) {
			continue // 剔除不完整词，不留悬挂指针
		}
		l, err := posting.Decode(posts[e.off : e.off+e.length])
		if err != nil {
			continue
		}
		s.Terms = append(s.Terms, e.term)
		s.Lists[e.term] = l
	}
	if truncated {
		return s, classify()
	}
	return s, nil
}
