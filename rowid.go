package ontology

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// contentID derives a deterministic identity from a row's own content,
// independent of arrival order and map iteration order. Keys are sorted,
// each pair is encoded as "key\x00<typetag>:<value>\x00", and the
// concatenation is hashed with FNV-1a 64. The canonical encoding itself is
// returned as the final tie-breaker for hash collisions.
func contentID(row map[string]any) (hash uint64, canon string) {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		v := row[k]
		fmt.Fprintf(&b, "%T:%v", v, v)
		b.WriteByte(0)
	}
	canon = b.String()

	h := fnv.New64a()
	_, _ = h.Write([]byte(canon))
	return h.Sum64(), canon
}
