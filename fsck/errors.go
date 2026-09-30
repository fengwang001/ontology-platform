package fsck

import "errors"

// Rejection reasons. Each maps to exactly one unrecoverable condition and
// leaves the original image byte-identical.
var (
	// ErrCorruptHeader: the image header (magic, version, checksum or
	// layout fields) is damaged.
	ErrCorruptHeader = errors.New("fsck: image header corrupt")
	// ErrRootNotDir: the root inode is not a directory.
	ErrRootNotDir = errors.New("fsck: root inode is not a directory")
	// ErrRecoveryNameConflict: the recovery directory name is occupied by
	// a non-directory inode.
	ErrRecoveryNameConflict = errors.New("fsck: recovery dir name occupied by non-directory")
	// ErrNoFreeInode: a recovery directory must be created but no free
	// inode remains.
	ErrNoFreeInode = errors.New("fsck: no free inode for recovery dir")
	// ErrMounted: the image is currently write-mounted.
	ErrMounted = errors.New("fsck: image is write-mounted")
	// ErrNoSpace: a directory needs to grow but no free data block or
	// block-list slot remains.
	ErrNoSpace = errors.New("fsck: no free data block for directory growth")
	// ErrInjected: a failure was injected for testing.
	ErrInjected = errors.New("fsck: injected failure")
)
