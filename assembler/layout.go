package assembler

import "fmt"

type UndefinedLabelError struct {
	InstructionIndex int
	Op               Op
	Label            string
}

func (e UndefinedLabelError) Error() string {
	return fmt.Sprintf("%s at instruction %d references undefined label %q", e.Op, e.InstructionIndex, e.Label)
}

func (e UndefinedLabelError) Unwrap() error {
	return ErrUndefinedLabel
}

type LayoutRound struct {
	Number       int
	Changed      []int
	Instructions []Instruction
	TotalLength  int
}

func (o Op) String() string {
	switch o {
	case PAD:
		return "PAD"
	case JMP:
		return "JMP"
	case JZ:
		return "JZ"
	default:
		return "UNKNOWN"
	}
}

func (f Form) String() string {
	switch f {
	case ShortForm:
		return "short"
	case LongForm:
		return "long"
	default:
		return "none"
	}
}

func AssembleRounds(snapshot Snapshot) (*Layout, []LayoutRound, error) {
	items := cloneItems(snapshot.Items)
	labels := cloneLabels(snapshot.Labels)
	if err := validateReferences(items, labels); err != nil {
		return nil, nil, err
	}

	forms := make([]Form, len(items))
	for index, item := range items {
		if isJump(item.Op) {
			forms[index] = ShortForm
		}
	}
	var rounds []LayoutRound

	for {
		instructions, totalLength := layout(items, labels, forms)
		changed := make([]int, 0)
		nextForms := cloneForms(forms)

		for index := range items {
			if isJump(items[index].Op) && forms[index] == ShortForm && !fitsShort(instructions[index].Offset) {
				nextForms[index] = LongForm
				changed = append(changed, index)
			}
		}

		rounds = append(rounds, LayoutRound{
			Number:       len(rounds) + 1,
			Changed:      changed,
			Instructions: instructions,
			TotalLength:  totalLength,
		})

		if len(changed) == 0 {
			return &Layout{
				Instructions: instructions,
				Labels:       labelAddresses(labels, instructions, totalLength),
				TotalLength:  totalLength,
			}, rounds, nil
		}
		forms = nextForms
	}
}

func Assemble(snapshot Snapshot) (*Layout, error) {
	layout, _, err := AssembleRounds(snapshot)
	return layout, err
}

func validateReferences(items []Item, labels map[string]int) error {
	for index, item := range items {
		if isJump(item.Op) {
			if _, ok := labels[item.Label]; !ok {
				return UndefinedLabelError{
					InstructionIndex: index,
					Op:               item.Op,
					Label:            item.Label,
				}
			}
		}
	}
	return nil
}

func layout(items []Item, labels map[string]int, forms []Form) ([]Instruction, int) {
	instructions := make([]Instruction, len(items))
	address := 0

	for index, item := range items {
		length := itemLength(item, index, forms)
		instructions[index] = Instruction{
			Index:  index,
			Op:     item.Op,
			Label:  item.Label,
			Start:  address,
			Length: length,
		}
		if isJump(item.Op) {
			instructions[index].Form = forms[index]
		}
		address += length
	}

	for index, item := range items {
		if !isJump(item.Op) {
			continue
		}
		target := labelAddress(labels[item.Label], instructions, address)
		instructions[index].Offset = target - (instructions[index].Start + instructions[index].Length)
	}

	return instructions, address
}

func itemLength(item Item, index int, forms []Form) int {
	switch item.Op {
	case PAD:
		return item.Bytes
	case JMP:
		if forms[index] == LongForm {
			return 5
		}
		return 2
	case JZ:
		if forms[index] == LongForm {
			return 6
		}
		return 2
	default:
		return 0
	}
}

func fitsShort(offset int) bool {
	return offset >= -128 && offset <= 127
}

func isJump(op Op) bool {
	return op == JMP || op == JZ
}

func cloneForms(forms []Form) []Form {
	cloned := make([]Form, len(forms))
	copy(cloned, forms)
	return cloned
}

func labelAddresses(labels map[string]int, instructions []Instruction, totalLength int) map[string]int {
	addresses := make(map[string]int, len(labels))
	for name, itemIndex := range labels {
		addresses[name] = labelAddress(itemIndex, instructions, totalLength)
	}
	return addresses
}

func labelAddress(itemIndex int, instructions []Instruction, totalLength int) int {
	if itemIndex >= len(instructions) {
		return totalLength
	}
	return instructions[itemIndex].Start
}
