package vma

import "errors"

var (
	ErrInvalid    = errors.New("vma: invalid argument")
	ErrExists     = errors.New("vma: mapping exists")
	ErrNoSpace    = errors.New("vma: no space available")
	ErrTooMany    = errors.New("vma: too many vmas")
	ErrNoMem      = errors.New("vma: not mapped")
	ErrMapped     = errors.New("vma: address already mapped")
	ErrSegv       = errors.New("vma: segmentation fault")
	ErrStackLimit = errors.New("vma: stack size limit exceeded")
	ErrNoRoom     = errors.New("vma: guard gap too small")
)
