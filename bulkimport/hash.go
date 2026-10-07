package bulkimport

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
)

// chunkHash computes a canonical content hash of a chunk. Duplicate
// detection depends only on the sequence number and this hash, never on
// arrival time. The hash is independent of entry order, field order and
// reference order inside the chunk.
func chunkHash(c Chunk) string {
	h := fnv.New64a()
	entries := make([]Entry, len(c.Entries))
	copy(entries, c.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, e := range entries {
		writeStr(h, e.ID)
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeLen(h, len(keys))
		for _, k := range keys {
			writeStr(h, k)
			writeStr(h, e.Fields[k])
		}
		refs := make([]string, len(e.Refs))
		copy(refs, e.Refs)
		sort.Strings(refs)
		writeLen(h, len(refs))
		for _, r := range refs {
			writeStr(h, r)
		}
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

func writeStr(w io.Writer, s string) {
	writeLen(w, len(s))
	_, _ = io.WriteString(w, s)
}

func writeLen(w io.Writer, n int) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(n))
	_, _ = w.Write(buf[:])
}
