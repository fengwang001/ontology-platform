package batch

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

var manifestMagic = []byte("ONTBATCH")

// EncodeManifest 将批次头与来源中的全部记录流式编码为二进制清单。
//
// 编码：8 字节魔数，uint32 记录数；每条记录为
// uint16 键长 + 键、uint32 值长 + 值。
func EncodeManifest(w io.Writer, id string, src Source) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.Write(manifestMagic); err != nil {
		return err
	}
	if len(id) > 0xffff {
		return fmt.Errorf("batch id too long")
	}
	var hdr [6]byte
	binary.LittleEndian.PutUint16(hdr[0:2], uint16(len(id)))
	binary.LittleEndian.PutUint32(hdr[2:6], uint32(src.Count()))
	if _, err := bw.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := bw.WriteString(id); err != nil {
		return err
	}
	var l [4]byte
	for i := 0; i < src.Count(); i++ {
		rec, err := src.At(i)
		if err != nil {
			return err
		}
		if len(rec.Key) > 0xffff {
			return fmt.Errorf("key too long at %d", i)
		}
		binary.LittleEndian.PutUint16(l[0:2], uint16(len(rec.Key)))
		if _, err := bw.Write(l[0:2]); err != nil {
			return err
		}
		if _, err := bw.WriteString(rec.Key); err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(l[0:4], uint32(len(rec.Value)))
		if _, err := bw.Write(l[0:4]); err != nil {
			return err
		}
		if _, err := bw.Write(rec.Value); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteManifest 将清单原子写入 path（临时文件 + rename）。
func WriteManifest(path, id string, src Source) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := EncodeManifest(f, id, src); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// FileSource 是清单文件的随机读取来源；解析仅在创建时流式进行一遍并记录偏移。
type FileSource struct {
	path    string
	id      string
	count   int
	offsets []int64
	f       *os.File
}

// OpenManifest 流式解析清单，建立每条记录的偏移索引。
func OpenManifest(path string) (*FileSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil {
		f.Close()
		return nil, err
	}
	if string(magic) != string(manifestMagic) {
		f.Close()
		return nil, fmt.Errorf("bad manifest magic")
	}
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		f.Close()
		return nil, err
	}
	idLen := binary.LittleEndian.Uint16(hdr[0:2])
	count := int(binary.LittleEndian.Uint32(hdr[2:6]))
	idb := make([]byte, idLen)
	if _, err := io.ReadFull(r, idb); err != nil {
		f.Close()
		return nil, err
	}
	s := &FileSource{path: path, id: string(idb), count: count, offsets: make([]int64, 0, count), f: f}
	off := int64(8 + 6 + len(idb))
	for i := 0; i < count; i++ {
		s.offsets = append(s.offsets, off)
		var l [4]byte
		if _, err := io.ReadFull(r, l[0:2]); err != nil {
			return nil, s.fail(err)
		}
		keyLen := int(binary.LittleEndian.Uint16(l[0:2]))
		key := make([]byte, keyLen)
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, s.fail(err)
		}
		if _, err := io.ReadFull(r, l[0:4]); err != nil {
			return nil, s.fail(err)
		}
		valLen := int64(binary.LittleEndian.Uint32(l[0:4]))
		if _, err := r.Discard(int(valLen)); err != nil {
			return nil, s.fail(err)
		}
		off += 2 + int64(keyLen) + 4 + valLen
	}
	return s, nil
}

func (s *FileSource) fail(err error) error {
	s.f.Close()
	return err
}

// ID 返回清单中的批次 ID。
func (s *FileSource) ID() string { return s.id }

// Count 实现 Source。
func (s *FileSource) Count() int { return s.count }

// At 实现 Source，按偏移随机读取一条记录。
func (s *FileSource) At(i int) (Record, error) {
	if i < 0 || i >= s.count {
		return Record{}, fmt.Errorf("record index %d out of range", i)
	}
	if _, err := s.f.Seek(s.offsets[i], io.SeekStart); err != nil {
		return Record{}, err
	}
	r := bufio.NewReader(s.f)
	var l [4]byte
	if _, err := io.ReadFull(r, l[0:2]); err != nil {
		return Record{}, err
	}
	key := make([]byte, binary.LittleEndian.Uint16(l[0:2]))
	if _, err := io.ReadFull(r, key); err != nil {
		return Record{}, err
	}
	if _, err := io.ReadFull(r, l[0:4]); err != nil {
		return Record{}, err
	}
	val := make([]byte, binary.LittleEndian.Uint32(l[0:4]))
	if _, err := io.ReadFull(r, val); err != nil {
		return Record{}, err
	}
	return Record{Key: string(key), Value: val}, nil
}

// Close 实现 Source。
func (s *FileSource) Close() error { return s.f.Close() }
