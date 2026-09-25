// Package persist serializes an LSH index to bytes and reads it back,
// classifying truncation and detecting corruption. Layout (little-endian):
// 22-byte header, tables*bits*dim float64 normals, bucket tables, CRC32.
package persist

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"slices"

	"ontology/bucket"
	"ontology/hyper"
)

// HeaderLen is the fixed header size in bytes.
const HeaderLen = 22

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrHeader      = errors.New("persist: header incomplete")
	ErrHyperplanes = errors.New("persist: hyperplane parameters incomplete")
	ErrBuckets     = errors.New("persist: bucket table incomplete")
	ErrCRC         = errors.New("persist: crc mismatch")
	ErrDegenerate  = errors.New("persist: degenerate hyperplane")
)

// DegenerateError reports an all-zero (non-partitioning) hyperplane normal.
type DegenerateError struct{ Table, Bit int }

func (e DegenerateError) Error() string {
	return fmt.Sprintf("persist: degenerate all-zero normal at table %d bit %d", e.Table, e.Bit)
}

func (e DegenerateError) Unwrap() error { return ErrDegenerate } // for errors.Is

// Encode serializes family + bucket tables, appending a CRC32 trailer.
func Encode(fam *hyper.Family, bk *bucket.Index, nvec int) []byte {
	buf := make([]byte, 0, 4096)
	buf = append(buf, "OLSH"...)
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	for _, u := range []int{fam.Dim(), fam.Bits(), fam.Tables(), nvec} {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(u))
	}
	for _, tbl := range fam.Normals() {
		for _, n := range tbl {
			for _, x := range n {
				buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(x))
			}
		}
	}
	for _, tbl := range bk.Snapshot() {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(tbl)))
		for sig, ids := range tbl {
			buf = binary.LittleEndian.AppendUint64(buf, sig)
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(ids)))
			for _, id := range ids {
				buf = binary.LittleEndian.AppendUint32(buf, uint32(id))
			}
		}
	}
	return binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

// Save writes the encoded index to path.
func Save(path string, fam *hyper.Family, bk *bucket.Index, nvec int) error {
	return os.WriteFile(path, Encode(fam, bk, nvec), 0o600)
}

func parseHeader(data []byte) (dim, bits, tables, nvec int, err error) {
	if len(data) < HeaderLen || string(data[:4]) != "OLSH" {
		return 0, 0, 0, 0, ErrHeader
	}
	u32 := func(off int) int { return int(binary.LittleEndian.Uint32(data[off:])) }
	return u32(6), u32(10), u32(14), u32(18), nil
}

func readNormals(data []byte, off, tables, bits, dim int) [][][]float64 {
	normals := make([][][]float64, tables)
	for t := range normals {
		normals[t] = make([][]float64, bits)
		for h := range normals[t] {
			n := make([]float64, dim)
			for i := range n {
				n[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[off:]))
				off += 8
			}
			normals[t][h] = n
		}
	}
	return normals
}

// parseBuckets decodes tables; in recover mode it keeps complete buckets and
// drops dangling IDs (id >= nvec), counting them in rec.
func parseBuckets(data []byte, off, tables, nvec int, rec *Recovered) ([]map[uint64][]int, int, error) {
	tbls := make([]map[uint64][]int, tables)
	truncated := func() ([]map[uint64][]int, int, error) {
		if rec != nil {
			return tbls, off, nil
		}
		return nil, 0, ErrBuckets
	}
	for t := 0; t < tables; t++ {
		tbls[t] = make(map[uint64][]int)
		if off+4 > len(data) {
			return truncated()
		}
		nb := int(binary.LittleEndian.Uint32(data[off:]))
		off += 4
		for b := 0; b < nb; b++ {
			if off+12 > len(data) {
				return truncated()
			}
			sig := binary.LittleEndian.Uint64(data[off:])
			cnt := int(binary.LittleEndian.Uint32(data[off+8:]))
			off += 12
			if off+4*cnt > len(data) {
				return truncated()
			}
			ids := make([]int, 0, cnt)
			for i := 0; i < cnt; i++ {
				id := int(binary.LittleEndian.Uint32(data[off:]))
				off += 4
				switch {
				case id < nvec:
					ids = append(ids, id)
				case rec != nil:
					rec.DroppedIDs++
				}
			}
			tbls[t][sig] = ids
			if rec != nil {
				rec.BucketsKept++
			}
		}
	}
	return tbls, off, nil
}

// Load strictly decodes an index, classifying truncation by region and
// rejecting CRC mismatches and degenerate (all-zero) hyperplane normals.
func Load(data []byte) (*hyper.Family, *bucket.Index, int, error) {
	dim, bits, tables, nvec, err := parseHeader(data)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(data) < HeaderLen+tables*bits*dim*8 {
		return nil, nil, 0, ErrHyperplanes
	}
	normals := readNormals(data, HeaderLen, tables, bits, dim)
	for t, tbl := range normals {
		for h, n := range tbl {
			if !slices.ContainsFunc(n, func(x float64) bool { return x != 0 }) {
				return nil, nil, 0, DegenerateError{Table: t, Bit: h}
			}
		}
	}
	tbls, off, err := parseBuckets(data, HeaderLen+tables*bits*dim*8, tables, nvec, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(data)-off < 4 ||
		crc32.ChecksumIEEE(data[:off]) != binary.LittleEndian.Uint32(data[off:]) {
		return nil, nil, 0, ErrCRC
	}
	return hyper.NewFrom(normals), bucket.NewFrom(tbls), nvec, nil
}

// Recovered is the maximal self-consistent prefix of a damaged index.
type Recovered struct {
	Family      *hyper.Family
	Buckets     *bucket.Index
	NVec        int
	DroppedIDs  int // dangling references removed (id >= NVec)
	BucketsKept int
}

// Recover reads as many complete buckets as possible and drops dangling IDs.
func Recover(data []byte) (*Recovered, error) {
	dim, bits, tables, nvec, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if len(data) < HeaderLen+tables*bits*dim*8 {
		return nil, ErrHyperplanes
	}
	rec := &Recovered{
		Family: hyper.NewFrom(readNormals(data, HeaderLen, tables, bits, dim)),
		NVec:   nvec,
	}
	tbls, _, _ := parseBuckets(data, HeaderLen+tables*bits*dim*8, tables, nvec, rec)
	rec.Buckets = bucket.NewFrom(tbls)
	return rec, nil
}
