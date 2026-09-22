// Package unknown is the container for fields a parser does not
// recognize. It preserves them verbatim, can sort them, and writes
// them back. It depends on nothing outside the standard library.
package unknown

import "sort"

// Field is one unrecognized field, kept as its complete raw encoding
// (header + length prefix + payload) so that rewriting is byte-exact
// for any input, including non-canonical varints.
type Field struct {
	Num  uint64 // field number from the header
	Type byte   // wire type byte from the header
	Raw  []byte // complete encoded field
}

// Fields is an ordered collection of unknown fields. The zero value
// is ready to use. Order of insertion is preserved, including
// duplicate field numbers: entries are never deduplicated or merged.
type Fields struct {
	list []Field
}

// Len returns the number of stored fields.
func (f *Fields) Len() int { return len(f.list) }

// At returns the i-th field. The returned Raw slice aliases internal
// storage; callers must treat it as read-only.
func (f *Fields) At(i int) Field { return f.list[i] }

// Add appends a field. raw is copied, so the caller may reuse or
// mutate its buffer afterwards.
func (f *Fields) Add(num uint64, typ byte, raw []byte) {
	cp := make([]byte, len(raw))
	copy(cp, raw)
	f.list = append(f.list, Field{Num: num, Type: typ, Raw: cp})
}

// Sort stably orders the stored fields by field number. Entries with
// equal numbers keep their relative order. Sorting is never done
// implicitly: the default iteration order is the original one.
func (f *Fields) Sort() {
	sort.SliceStable(f.list, func(i, j int) bool {
		return f.list[i].Num < f.list[j].Num
	})
}

// AppendTo appends the raw encoding of every field, in order, to dst
// and returns the extended slice. It does not modify the container.
func (f *Fields) AppendTo(dst []byte) []byte {
	for _, fld := range f.list {
		dst = append(dst, fld.Raw...)
	}
	return dst
}
