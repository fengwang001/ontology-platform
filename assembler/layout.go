package assembler

func assemble(ops []Record, labels map[string]int) (*Result, error) {
	for index, op := range ops {
		if op.Kind == JMP || op.Kind == JZ {
			if _, defined := labels[op.Label]; !defined {
				return nil, UndefinedLabelError{OperationIndex: index, Label: op.Label}
			}
		}
	}

	forms := make([]Form, len(ops))
	for index, op := range ops {
		if op.Kind == Pad {
			forms[index] = NoForm
		} else {
			forms[index] = ShortForm
		}
	}

	rounds := 0
	for {
		entries, labelAddresses := layout(ops, labels, forms)
		nextForms := append([]Form(nil), forms...)
		changed := false

		for _, entry := range entries {
			if entry.Form == ShortForm && (entry.Offset < -128 || entry.Offset > 127) {
				nextForms[entry.Index] = LongForm
				changed = true
			}
		}

		if !changed {
			return &Result{
				Entries:     entries,
				Labels:      labelAddresses,
				TotalLength: totalSize(ops, forms),
				Rounds:      rounds,
			}, nil
		}

		forms = nextForms
		rounds++
	}
}

func instructionSize(kind OpKind, form Form) int {
	switch kind {
	case Pad:
		return 0
	case JMP:
		if form == LongForm {
			return 5
		}
		return 2
	case JZ:
		if form == LongForm {
			return 6
		}
		return 2
	default:
		return 0
	}
}

func layout(ops []Record, labels map[string]int, forms []Form) ([]Entry, map[string]int) {
	labelAddresses := make(map[string]int, len(labels))
	entries := make([]Entry, 0, len(ops))

	emitLabels := func(index, address int) {
		for name, boundIndex := range labels {
			if boundIndex == index {
				labelAddresses[name] = address
			}
		}
	}

	address := 0
	emitLabels(0, address)
	for index, op := range ops {
		size := op.PadBytes
		if op.Kind != Pad {
			size = instructionSize(op.Kind, forms[index])
		}

		entry := Entry{
			Index:    index,
			Kind:     op.Kind,
			PadBytes: op.PadBytes,
			Label:    op.Label,
			Start:    address,
			Size:     size,
			Form:     forms[index],
			Target:   -1,
		}

		entries = append(entries, entry)
		address += size
		emitLabels(index+1, address)
	}

	for index := range entries {
		entry := &entries[index]
		if entry.Kind == JMP || entry.Kind == JZ {
			entry.Target = labelAddresses[entry.Label]
			entry.Offset = entry.Target - (entry.Start + entry.Size)
		}
	}

	return entries, labelAddresses
}

func totalSize(ops []Record, forms []Form) int {
	total := 0
	for index, op := range ops {
		if op.Kind == Pad {
			total += op.PadBytes
		} else {
			total += instructionSize(op.Kind, forms[index])
		}
	}
	return total
}
