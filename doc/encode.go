package doc

import (
	"encoding/binary"
	"io"
	"math"
	"sort"
	"strconv"
)

func sortStrings(xs []string) { sort.Strings(xs) }

func encBytes(b, s []byte) []byte {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(s)))
	b = append(b, l[:]...)
	return append(b, s...)
}

// EncodeRecord 写出单条记录的规范化编码（不含键）。
func EncodeRecord(b []byte, r Record) []byte {
	fs := r.SortedFields()
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(fs)))
	b = append(b, n[:]...)
	for _, f := range fs {
		b = append(b, 'f')
		b = encBytes(b, []byte(f))
		v := r[f]
		if v.IsNumber() {
			b = append(b, 'n')
			var u [8]byte
			binary.BigEndian.PutUint64(u[:], math.Float64bits(v.AsNumber()))
			b = append(b, u[:]...)
		} else {
			b = append(b, 's')
			b = encBytes(b, strconv.AppendQuote(nil, v.AsString()))
		}
	}
	return b
}

// EncodeSet 写出集合的规范化编码：键字典序，记录前加 'r' 与长度前缀的键。
func EncodeSet(s Set) []byte {
	var b []byte
	for _, k := range s.SortedKeys() {
		b = append(b, 'r')
		b = encBytes(b, []byte(k))
		b = EncodeRecord(b, s[k])
	}
	return b
}

func decBytes(buf []byte) ([]byte, []byte, error) {
	if len(buf) < 4 {
		return nil, nil, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint32(buf[:4]))
	buf = buf[4:]
	if len(buf) < n {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return buf[n:], buf[:n], nil
}

func decU32(buf []byte) ([]byte, uint32, error) {
	if len(buf) < 4 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	return buf[4:], binary.BigEndian.Uint32(buf[:4]), nil
}

// DecodeSet 是 EncodeSet 的逆操作。
func DecodeSet(buf []byte) (Set, error) {
	s := Set{}
	for len(buf) > 0 {
		if buf[0] != 'r' {
			return nil, io.ErrUnexpectedEOF
		}
		buf = buf[1:]
		var key []byte
		var err error
		buf, key, err = decBytes(buf)
		if err != nil {
			return nil, err
		}
		r := Record{}
		buf, r, err = decodeRecordInto(buf, r)
		if err != nil {
			return nil, err
		}
		s[string(key)] = r
	}
	return s, nil
}

func decodeRecordInto(buf []byte, r Record) ([]byte, Record, error) {
	var nf uint32
	var err error
	buf, nf, err = decU32(buf)
	if err != nil {
		return nil, nil, err
	}
	for i := uint32(0); i < nf; i++ {
		if len(buf) == 0 || buf[0] != 'f' {
			return nil, nil, io.ErrUnexpectedEOF
		}
		buf = buf[1:]
		var name []byte
		buf, name, err = decBytes(buf)
		if err != nil {
			return nil, nil, err
		}
		if len(buf) == 0 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		t := buf[0]
		buf = buf[1:]
		switch t {
		case 'n':
			if len(buf) < 8 {
				return nil, nil, io.ErrUnexpectedEOF
			}
			r[string(name)] = Number(math.Float64frombits(binary.BigEndian.Uint64(buf[:8])))
			buf = buf[8:]
		case 's':
			var raw []byte
			buf, raw, err = decBytes(buf)
			if err != nil {
				return nil, nil, err
			}
			str, qerr := strconv.Unquote(string(raw))
			if qerr != nil {
				return nil, nil, io.ErrUnexpectedEOF
			}
			r[string(name)] = String(str)
		default:
			return nil, nil, io.ErrUnexpectedEOF
		}
	}
	return buf, r, nil
}
