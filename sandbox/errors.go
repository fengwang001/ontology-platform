package sandbox

import "errors"

var (
	ErrEmptyPath         = errors.New("sandbox: path is empty")
	ErrInvalidName       = errors.New("sandbox: name is empty or contains slash")
	ErrNotExist          = errors.New("sandbox: path segment does not exist")
	ErrExists            = errors.New("sandbox: name already exists")
	ErrNotDirectory      = errors.New("sandbox: intermediate segment is not a directory")
	ErrDirectoryNotEmpty = errors.New("sandbox: directory not empty")
	ErrInvalidArgument   = errors.New("sandbox: invalid argument")
	ErrTooManyLinks      = errors.New("sandbox: too many symbolic links (>40)")
	ErrRenameIntoSelf    = errors.New("sandbox: cannot rename directory into itself or a subdirectory")
)
