package ontology

import "errors"

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrCommitNotFound      = errors.New("commit not found")
	ErrListVersionNotFound = errors.New("ignore-list version not found")
	ErrPathNotFound        = errors.New("path not found")
	ErrDuplicateCommit     = errors.New("duplicate commit")
	ErrParentNotFound      = errors.New("parent commit not found")
	ErrInvalidRename       = errors.New("invalid rename")
)
