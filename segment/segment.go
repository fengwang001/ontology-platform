// Package segment defines the segment file format (self-describing header,
// dictionary records interleaved with posting lists, CRC32 footer) and
// its atomic writer.
package segment

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"sort"

	"ontology/posting"
)

// HeaderSize is the fixed header length: magic(8) version(4) count(4) bodyLen(8).
const HeaderSize = 24

var magic = []byte("ONTSEG01")

const version = 1

// Segment is an in-memory view of a segment file.
type Segment struct {
	terms []string
	lists map[string]posting.List
}

// Terms returns the dictionary terms in sorted order.
func (s *Segment) Terms() []string { return s.terms }

// Postings returns the posting list of a term.
func (s *Segment) Postings(term string) (posting.List, bool) {
	l, ok := s.lists[term]
	return l, ok
}

// Docs returns the sorted union of document IDs in the segment.
func (s *Segment) Docs() []uint32 {
	var docs []uint32
	for _, t := range s.terms {
		docs = append(docs, s.lists[t].Docs()...)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i] < docs[j] })
	return docs
}

// Write atomically writes a segment file for the given term lists
// (write to tmp file, then rename over the target).
func Write(path string, lists map[string]posting.List) error {
	terms := make([]string, 0, len(lists))
	for t := range lists {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	var body []byte
	var buf [binary.MaxVarintLen64]byte
	putUvarint := func(v uint64) {
		n := binary.PutUvarint(buf[:], v)
		body = append(body, buf[:n]...)
	}
	for _, t := range terms {
		enc := posting.Encode(lists[t])
		putUvarint(uint64(len(t)))
		body = append(body, t...)
		putUvarint(uint64(len(enc)))
		body = append(body, enc...)
	}

	header := make([]byte, HeaderSize)
	copy(header, magic)
	binary.LittleEndian.PutUint32(header[8:], version)
	binary.LittleEndian.PutUint32(header[12:], uint32(len(terms)))
	binary.LittleEndian.PutUint64(header[16:], uint64(len(body)))

	crc := crc32.NewIEEE()
	crc.Write(header)
	crc.Write(body)
	var footer [4]byte
	binary.LittleEndian.PutUint32(footer[:], crc.Sum32())

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, b := range [][]byte{header, body, footer[:]} {
		if _, err := f.Write(b); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
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
