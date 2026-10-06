package ontology

func calculateLayout(config Config, spec TypeSpec, lookup func(string) (Layout, bool)) (Layout, error) {
	layout := Layout{Name: spec.Name, Fields: make([]FieldLayout, 0, len(spec.Fields))}
	for _, field := range spec.Fields {
		size := config.PointerSize
		alignment := config.PointerAlignment
		if !field.Pointer {
			target, ok := lookup(field.Type)
			if !ok {
				return Layout{}, typeError(ErrUndefinedType, spec.Name, errUndefinedType)
			}
			size = target.Size
			alignment = target.Align
		}
		if !field.Compact {
			alignment = cappedAlignment(alignment, spec.MaxAlign)
		}
		offset := layout.Size
		if !field.Compact && alignment > 0 {
			offset = roundUp(offset, alignment)
		}
		layout.Fields = append(layout.Fields, FieldLayout{
			Name:    field.Name,
			Type:    field.Type,
			Offset:  offset,
			Size:    size,
			Align:   alignment,
			Compact: field.Compact,
			Pointer: field.Pointer,
		})
		layout.Size = offset + size
		if !field.Compact && alignment > layout.Align {
			layout.Align = alignment
		}
	}
	if len(spec.Fields) == 0 {
		layout.Size = config.MinCompositeSize
		layout.Align = config.MinCompositeAlign
	}
	if layout.Align == 0 {
		layout.Align = config.MinCompositeAlign
	}
	layout.Size = roundUp(layout.Size, layout.Align)
	return layout, nil
}

func roundUp(value, alignment int) int {
	if alignment <= 1 {
		return value
	}
	return (value + alignment - 1) / alignment * alignment
}

func allowedAlignment(alignment int, allowed map[int]bool) bool {
	if len(allowed) == 0 {
		return alignment > 0
	}
	return allowed[alignment]
}

func cappedAlignment(alignment int, typeCap int) int {
	if typeCap > 0 && typeCap < alignment {
		return typeCap
	}
	return alignment
}
