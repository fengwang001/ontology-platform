package layout

// alignUp rounds offset up to a multiple of align (align must be positive).
func alignUp(offset, align int) int {
	r := offset % align
	if r != 0 {
		offset += align - r
	}
	return offset
}

// fieldSizeAlign returns the size and natural alignment of a field from the
// resolved layouts of its dependencies. For indirect fields both come from
// the configuration, so target may be nil; embedded fields require a
// registered target layout.
func fieldSizeAlign(cfg Config, f Field, target *Layout) (size, align int) {
	if f.Indirect {
		return cfg.PtrSize, cfg.PtrAlign
	}
	return target.Size, target.Align
}

// computeLayout resolves offsets, total size and alignment for one composite
// type. It is a pure function of the spec and the current layouts of its
// directly embedded dependencies: its cost is O(len(spec.Fields)) and it
// never inspects unrelated types. Indirect targets need no layout.
//
// The caller guarantees that embedded targets are registered and that the
// embedding graph is acyclic.
func computeLayout(cfg Config, spec CompositeSpec, lookup func(name string) *Layout) (*Layout, error) {
	out := &Layout{Name: spec.Name, Version: 1, Fields: make([]FieldLayout, 0, len(spec.Fields))}

	offset := 0
	maxAlign := 0
	for _, f := range spec.Fields {
		var target *Layout
		if !f.Indirect {
			target = lookup(f.TypeName)
			if target == nil {
				return nil, ErrUndefinedType
			}
		}
		size, align := fieldSizeAlign(cfg, f, target)
		effective := align
		if spec.MaxAlign > 0 && effective > spec.MaxAlign {
			effective = spec.MaxAlign
		}
		if !f.Compact {
			offset = alignUp(offset, effective)
			if effective > maxAlign {
				maxAlign = effective
			}
		}
		out.Fields = append(out.Fields, FieldLayout{
			Name:     f.Name,
			TypeName: f.TypeName,
			Offset:   offset,
			Size:     size,
			Align:    align,
			Indirect: f.Indirect,
			Compact:  f.Compact,
		})
		offset += size
	}

	if len(spec.Fields) == 0 {
		out.Size = cfg.EmptySize
		out.Align = cfg.EmptyAlign
	} else {
		if maxAlign == 0 { // only compact fields: the type needs no alignment
			maxAlign = cfg.minAllowed()
		}
		out.Align = maxAlign
		out.Size = alignUp(offset, maxAlign)
	}
	if out.Size > cfg.MaxSize {
		return nil, ErrSizeExceeded
	}
	return out, nil
}
