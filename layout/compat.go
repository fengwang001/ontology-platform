package layout

// classify compares an old and a new layout of the same type and returns the
// binary-compatibility verdict together with a human-readable reason (used in
// logs). It is a pure O(len(old.Fields)) comparison:
//
//   - fields are matched positionally, so a swap is incompatible even if the
//     resulting offsets coincide;
//   - every old field must keep name, referenced type, offset and size;
//   - total size must never shrink and alignment must never change;
//   - exact equality (no new fields, same size) is fully compatible;
//   - additional trailing fields with strictly growing size are append
//     compatible; anything else is incompatible.
func classify(old, new *Layout) (CompatKind, string) {
	if old.Align != new.Align {
		return Incompatible, "alignment changed"
	}
	if new.Size < old.Size {
		return Incompatible, "size shrunk"
	}
	if len(new.Fields) < len(old.Fields) {
		return Incompatible, "field removed"
	}
	for i := range old.Fields {
		of, nf := old.Fields[i], new.Fields[i]
		switch {
		case of.Name != nf.Name:
			return Incompatible, "old field renamed/removed at index " + itoa(i)
		case of.TypeName != nf.TypeName:
			return Incompatible, "field type changed at index " + itoa(i)
		case of.Offset != nf.Offset:
			return Incompatible, "field offset changed at index " + itoa(i)
		case of.Size != nf.Size:
			return Incompatible, "field size changed at index " + itoa(i)
		case of.Indirect != nf.Indirect:
			return Incompatible, "field indirection changed at index " + itoa(i)
		case of.Compact != nf.Compact:
			return Incompatible, "field compact flag changed at index " + itoa(i)
		}
	}
	switch {
	case new.Size == old.Size && len(new.Fields) == len(old.Fields):
		return FullyCompatible, "all fields and size unchanged"
	case len(new.Fields) > len(old.Fields) && new.Size > old.Size:
		return AppendCompatible, itoa(len(new.Fields)-len(old.Fields)) + " trailing field(s) appended"
	default:
		// Covers: same size but fields added (tail padding filled), fields
		// removed, size shrink and any other non-append change.
		return Incompatible, "non-append structural change"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
