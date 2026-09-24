// Package store 提供单副本的进程内键值存储与本地写入。
package store

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sort"

	"ontology/vv"
)

var (
	ErrBadMaxSiblings = errors.New("store: max siblings must be >= 1")
	ErrHeader         = errors.New("store: incomplete header")
	ErrEntry          = errors.New("store: incomplete entry payload")
	ErrCRC            = errors.New("store: crc mismatch")
)

// Entry 是一个带版本向量的键值版本。
type Entry struct {
	Vec   vv.Vector
	Value string
}

// Store 是单个副本的全部状态。
type Store struct {
	id      string
	known   map[string]struct{}
	maxSib  int
	data    map[string][]Entry // 兄弟版本，始终按 Value 字典序升序
	water   vv.Vector          // 已见条目的逐分量最大值
	dropped int64
}

// New 创建副本 id 的存储；known 非空时校验所有副本 ID。
func New(id string, known []string, maxSiblings int) (*Store, error) {
	if maxSiblings < 1 {
		return nil, ErrBadMaxSiblings
	}
	m := &Store{id: id, maxSib: maxSiblings, data: map[string][]Entry{}, water: vv.Vector{}}
	if len(known) > 0 {
		m.known = map[string]struct{}{}
		for _, k := range known {
			m.known[k] = struct{}{}
		}
	}
	return m, nil
}

// ID 返回副本 ID。
func (s *Store) ID() string { return s.id }

// Watermark 返回已见水位的拷贝。
func (s *Store) Watermark() vv.Vector { return s.water.Copy() }

// Siblings 返回某 key 的兄弟版本（已按 Value 排序）。
func (s *Store) Siblings(key string) []Entry {
	src := s.data[key]
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// Dropped 返回因超过兄弟上限而丢弃的次数。
func (s *Store) Dropped() int64 { return s.dropped }

// Put 执行本地写入：本副本计数器 +1，覆盖该 key 的旧版本。
func (s *Store) Put(key, value string) (Entry, error) {
	if err := s.checkID(s.id); err != nil {
		return Entry{}, err
	}
	nv, err := vv.Inc(s.water, s.id)
	if err != nil {
		return Entry{}, err
	}
	s.water = nv
	e := Entry{Vec: nv.Copy(), Value: value}
	s.data[key] = []Entry{e}
	return e, nil
}

func (s *Store) checkID(id string) error {
	if len(s.known) > 0 {
		if _, ok := s.known[id]; !ok {
			return vv.ErrUnknownReplica
		}
	}
	return nil
}

func sortEntries(in []Entry) {
	sort.Slice(in, func(i, j int) bool { return in[i].Value < in[j].Value })
}

// EncodeEntry：magic="EN" | vlen uint32 | vec | klen uint16 | key | vlen2 uint16 | value | crc32
func EncodeEntry(key string, e Entry) []byte {
	vb := e.Vec.Encode()
	buf := make([]byte, 0, 4+len(vb)+2+len(key)+2+len(e.Value)+4)
	buf = append(buf, 'E', 'N')
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(vb)))
	buf = append(buf, vb...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(key)))
	buf = append(buf, key...)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(e.Value)))
	buf = append(buf, e.Value...)
	return binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

// DecodeEntry 解析 EncodeEntry，并按截断位置分类错误。
func DecodeEntry(data []byte) (string, Entry, error) {
	if len(data) < 4 || data[0] != 'E' || data[1] != 'N' {
		return "", Entry{}, ErrHeader
	}
	vl := int(binary.BigEndian.Uint32(data[2:6]))
	p := 6
	if len(data)-p < vl {
		return "", Entry{}, ErrEntry
	}
	vec, err := vv.Decode(data[p : p+vl])
	if err != nil {
		return "", Entry{}, ErrEntry
	}
	p += vl
	if len(data)-p < 2 {
		return "", Entry{}, ErrEntry
	}
	kl := int(binary.BigEndian.Uint16(data[p : p+2]))
	p += 2
	if len(data)-p < kl {
		return "", Entry{}, ErrEntry
	}
	key := string(data[p : p+kl])
	p += kl
	if len(data)-p < 2 {
		return "", Entry{}, ErrEntry
	}
	valLen := int(binary.BigEndian.Uint16(data[p : p+2]))
	p += 2
	if len(data)-p < valLen {
		return "", Entry{}, ErrEntry
	}
	val := string(data[p : p+valLen])
	p += valLen
	if len(data)-p < 4 {
		return "", Entry{}, ErrCRC
	}
	want := binary.BigEndian.Uint32(data[p : p+4])
	if crc32.ChecksumIEEE(data[:p]) != want {
		return "", Entry{}, ErrCRC
	}
	return key, Entry{Vec: vec, Value: val}, nil
}
