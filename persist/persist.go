// Package persist serialises an LSH index structure to a local file and
// reads it back with structural validation, CRC32 integrity checks and
// maximum-prefix recovery from truncated files.
package persist

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"sort"
)

// Errors classifying corrupted or truncated index files; use errors.Is.
var (
	ErrHeaderTruncated = errors.New("persist: header incomplete")
	ErrHyperTruncated  = errors.New("persist: hyperplane parameters incomplete")
	ErrBucketTruncated = errors.New("persist: bucket table incomplete")
	ErrCRCMismatch     = errors.New("persist: crc32 mismatch")
	ErrDegeneratePlane = errors.New("persist: degenerate hyperplane (zero normal)")
)

var magic = []byte("LSH1")

// headerLen is magic + 4 uint32 fields + uint64 seed.
const headerLen = 4 + 4*4 + 8

// Data is the serialisable LSH index structure (vectors stay in memory).
type Data struct {
	Dim, Bits, NTables, NVecs int
	Seed                      int64
	Planes                    [][][]float64
	Tables                    []map[uint64][]int
}

// Recovered summarises a prefix recovery: fully recovered tables, total
// recovered buckets and dangling IDs that were dropped.
type Recovered struct {
	Tables  int
	Buckets int
	Dropped int
}

type cursor struct {
	buf []byte
	off int
}

func (c *cursor) take(n int, region error) ([]byte, error) {
	if n < 0 || len(c.buf)-c.off < n {
		return nil, region
	}
	b := c.buf[c.off : c.off+n]
	c.off += n
	return b, nil
}

func (c *cursor) u32(region error) (uint32, error) {
	b, err := c.take(4, region)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func putU32(buf *bytes.Buffer, v uint32) {
	buf.Write(binary.LittleEndian.AppendUint32(nil, v))
}

func putU64(buf *bytes.Buffer, v uint64) {
	buf.Write(binary.LittleEndian.AppendUint64(nil, v))
}

// Write serialises d with a trailing CRC32 over all preceding bytes.
func Write(path string, d *Data) error {
	buf := &bytes.Buffer{}
	buf.Write(magic)
	for _, v := range []int{d.Dim, d.Bits, d.NTables, d.NVecs} {
		putU32(buf, uint32(v))
	}
	putU64(buf, uint64(d.Seed))
	for _, tab := range d.Planes {
		for _, p := range tab {
			for _, x := range p {
				putU64(buf, math.Float64bits(x))
			}
		}
	}
	for _, tab := range d.Tables {
		sigs := make([]uint64, 0, len(tab))
		for sig := range tab {
			sigs = append(sigs, sig)
		}
		sort.Slice(sigs, func(i, j int) bool { return sigs[i] < sigs[j] })
		putU32(buf, uint32(len(tab)))
		for _, sig := range sigs {
			putU64(buf, sig)
			putU32(buf, uint32(len(tab[sig])))
			for _, id := range tab[sig] {
				putU32(buf, uint32(id))
			}
		}
	}
	putU32(buf, crc32.ChecksumIEEE(buf.Bytes()))
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func parseHeader(c *cursor) (*Data, error) {
	b, err := c.take(headerLen, ErrHeaderTruncated)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(b[:4], magic) {
		return nil, fmt.Errorf("%w: bad magic", ErrHeaderTruncated)
	}
	u := func(off int) int { return int(binary.LittleEndian.Uint32(b[off:])) }
	return &Data{Dim: u(4), Bits: u(8), NTables: u(12), NVecs: u(16),
		Seed: int64(binary.LittleEndian.Uint64(b[20:]))}, nil
}

func parsePlanes(c *cursor, d *Data) error {
	d.Planes = make([][][]float64, d.NTables)
	for t := 0; t < d.NTables; t++ {
		tab := make([][]float64, d.Bits)
		for i := 0; i < d.Bits; i++ {
			p := make([]float64, d.Dim)
			for j := range p {
				b, err := c.take(8, ErrHyperTruncated)
				if err != nil {
					return err
				}
				p[j] = math.Float64frombits(binary.LittleEndian.Uint64(b))
			}
			tab[i] = p
		}
		d.Planes[t] = tab
	}
	return nil
}

func validatePlanes(d *Data) error {
	for t, tab := range d.Planes {
		for i, p := range tab {
			zero := true
			for _, x := range p {
				if x != 0 {
					zero = false
					break
				}
			}
			if zero {
				return fmt.Errorf("%w: table %d bit %d", ErrDegeneratePlane, t, i)
			}
		}
	}
	return nil
}

func parseTables(c *cursor, d *Data) error {
	d.Tables = make([]map[uint64][]int, d.NTables)
	for t := 0; t < d.NTables; t++ {
		nb, err := c.u32(ErrBucketTruncated)
		if err != nil {
			return err
		}
		m := make(map[uint64][]int, nb)
		for i := 0; i < int(nb); i++ {
			b, err := c.take(12, ErrBucketTruncated)
			if err != nil {
				return err
			}
			sig := binary.LittleEndian.Uint64(b)
			cnt := int(binary.LittleEndian.Uint32(b[8:]))
			ids := make([]int, cnt)
			for j := range ids {
				id, err := c.u32(ErrBucketTruncated)
				if err != nil {
					return err
				}
				ids[j] = int(id)
			}
			m[sig] = ids
		}
		d.Tables[t] = m
	}
	return nil
}

// Read parses a complete index file; structural validation runs before
// the CRC check so a zeroed hyperplane is reported as degenerate.
func Read(path string) (*Data, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &cursor{buf: raw}
	d, err := parseHeader(c)
	if err != nil {
		return nil, err
	}
	if err := parsePlanes(c, d); err != nil {
		return nil, err
	}
	if err := validatePlanes(d); err != nil {
		return nil, err
	}
	if err := parseTables(c, d); err != nil {
		return nil, err
	}
	crcB, err := c.take(4, ErrCRCMismatch)
	if err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(raw[:len(raw)-4]) != binary.LittleEndian.Uint32(crcB) {
		return nil, ErrCRCMismatch
	}
	return d, nil
}

// Recover parses the largest self-consistent prefix of a possibly
// truncated file and drops dangling IDs (>= NVecs), counting them.
func Recover(path string) (*Data, *Recovered, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	rec := &Recovered{}
	c := &cursor{buf: raw}
	d, err := parseHeader(c)
	if err != nil {
		return nil, nil, err
	}
	if err := parsePlanes(c, d); err != nil {
		return d, rec, nil
	}
	if err := validatePlanes(d); err != nil {
		return d, rec, err
	}
	for t := 0; t < d.NTables; t++ {
		nb, err := c.u32(ErrBucketTruncated)
		if err != nil {
			break
		}
		m := make(map[uint64][]int)
		full := true
		for i := 0; i < int(nb); i++ {
			b, err := c.take(12, ErrBucketTruncated)
			if err != nil {
				full = false
				break
			}
			sig := binary.LittleEndian.Uint64(b)
			cnt := int(binary.LittleEndian.Uint32(b[8:]))
			if len(c.buf)-c.off < 4*cnt {
				full = false
				break
			}
			ids := make([]int, 0, cnt)
			for j := 0; j < cnt; j++ {
				id, _ := c.u32(ErrBucketTruncated)
				if int(id) < d.NVecs {
					ids = append(ids, int(id))
				} else {
					rec.Dropped++
				}
			}
			m[sig] = ids
			rec.Buckets++
		}
		d.Tables = append(d.Tables, m)
		if full {
			rec.Tables++
		} else {
			break
		}
	}
	return d, rec, nil
}
