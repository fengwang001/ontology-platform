package ontology

import "fmt"

type Compatibility int

const (
	Incompatible Compatibility = iota
	FullyCompatible
	AppendCompatible
)

func (c Compatibility) String() string {
	switch c {
	case FullyCompatible:
		return "fully-compatible"
	case AppendCompatible:
		return "append-compatible"
	default:
		return "incompatible"
	}
}

func compareLayouts(oldLayout, newLayout Layout) Compatibility {
	if oldLayout.Align != newLayout.Align {
		return Incompatible
	}
	if len(newLayout.Fields) < len(oldLayout.Fields) {
		return Incompatible
	}
	for index := range oldLayout.Fields {
		if !sameField(oldLayout.Fields[index], newLayout.Fields[index]) {
			return Incompatible
		}
	}
	if len(newLayout.Fields) == len(oldLayout.Fields) {
		if newLayout.Size != oldLayout.Size {
			return Incompatible
		}
		return FullyCompatible
	}
	if newLayout.Size <= oldLayout.Size {
		return Incompatible
	}
	return AppendCompatible
}

func sameField(oldField, newField FieldLayout) bool {
	return oldField.Name == newField.Name &&
		oldField.Type == newField.Type &&
		oldField.Offset == newField.Offset &&
		oldField.Size == newField.Size &&
		oldField.Compact == newField.Compact &&
		oldField.Pointer == newField.Pointer
}

func compatibilityReason(result Compatibility, oldLayout, newLayout Layout) string {
	if result == FullyCompatible {
		return fmt.Sprintf("all %d fields, size %d and alignment %d are unchanged", len(oldLayout.Fields), oldLayout.Size, oldLayout.Align)
	}
	if result == AppendCompatible {
		return fmt.Sprintf("first %d fields are unchanged and %d fields were appended; size %d->%d, alignment %d", len(oldLayout.Fields), len(newLayout.Fields)-len(oldLayout.Fields), oldLayout.Size, newLayout.Size, newLayout.Align)
	}
	return "field identity/order/type/compact flag/offset/size or layout boundary changed"
}
