package assembler

import (
	"errors"
	"strconv"
)

var (
	ErrInvalidPadSize      = errors.New("invalid PAD size")
	ErrEmptyLabelName      = errors.New("label name is empty")
	ErrLabelAlreadyDefined = errors.New("label is already defined")
	ErrUndefinedLabel      = errors.New("undefined label")
	ErrInvalidJumpKind     = errors.New("invalid jump kind")
)

type UndefinedLabelError struct {
	OperationIndex int
	Label          string
}

type InvalidPadSizeError struct {
	Size int
}

func (e InvalidPadSizeError) Error() string {
	return "PAD size must be between 1 and 1000"
}

func (e InvalidPadSizeError) Is(target error) bool {
	return target == ErrInvalidPadSize
}

type EmptyLabelNameError struct{}

func (e EmptyLabelNameError) Error() string {
	return ErrEmptyLabelName.Error()
}

func (e EmptyLabelNameError) Is(target error) bool {
	return target == ErrEmptyLabelName
}

type LabelAlreadyDefinedError struct {
	Name string
}

func (e LabelAlreadyDefinedError) Error() string {
	return "label is already defined: " + e.Name
}

func (e LabelAlreadyDefinedError) Is(target error) bool {
	return target == ErrLabelAlreadyDefined
}

func (e UndefinedLabelError) Error() string {
	return "undefined label at operation " + strconv.Itoa(e.OperationIndex) + ": " + e.Label
}

func (e UndefinedLabelError) Is(target error) bool {
	return target == ErrUndefinedLabel
}
