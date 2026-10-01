package fsck

import (
	"os"
	"sync"
)

// Checker guards one image file. Read-only checks run concurrently with
// each other; repair and write-mounts are mutually exclusive with
// everything else.
type Checker struct {
	mu   sync.RWMutex
	path string

	// FailBeforeCommit, when set, is invoked by Repair after the repaired
	// image has been fully written to the temporary file but before the
	// atomic rename. A non-nil return aborts the repair and leaves the
	// original image byte-identical. It exists to inject interruptions.
	FailBeforeCommit func() error
}

// Open returns a Checker for the image at path.
func Open(path string) *Checker {
	return &Checker{path: path}
}

// Path returns the image file path.
func (c *Checker) Path() string {
	return c.path
}

func refuse(img *Image) error {
	if img.RootInode >= img.InodeCount || img.Inodes[img.RootInode].Type != TypeDir {
		return ErrRootNotDir
	}
	if img.Flags&FlagMounted != 0 {
		return ErrMounted
	}
	return nil
}

// Check performs a read-only consistency scan. Multiple checks may run
// concurrently. It refuses corrupt, non-directory-root or mounted images.
func (c *Checker) Check() (*Report, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	img, err := ParseImage(data)
	if err != nil {
		return nil, err
	}
	if err := refuse(img); err != nil {
		return nil, err
	}
	return &Report{Findings: check(img)}, nil
}

// Repair checks the image, fixes every inconsistency on a private copy and
// atomically replaces the image file. On any refusal or injected failure
// the original file remains byte-identical.
func (c *Checker) Repair() (*RepairResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	img, err := ParseImage(data)
	if err != nil {
		return nil, err
	}
	if err := refuse(img); err != nil {
		return nil, err
	}
	work := img.Clone()
	res, err := repair(work)
	if err != nil {
		return nil, err
	}
	out := work.Bytes()

	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if c.FailBeforeCommit != nil {
		if err := c.FailBeforeCommit(); err != nil {
			os.Remove(tmp)
			return nil, err
		}
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return res, nil
}

// MountWrite marks the image write-mounted and blocks any concurrent
// Check/Repair until the returned unmount function is called. A second
// mount while the on-disk flag is set fails with ErrMounted.
func (c *Checker) MountWrite() (func(), error) {
	c.mu.Lock()
	data, err := os.ReadFile(c.path)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	img, err := ParseImage(data)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if img.Flags&FlagMounted != 0 {
		c.mu.Unlock()
		return nil, ErrMounted
	}
	img.Flags |= FlagMounted
	if err := os.WriteFile(c.path, img.Bytes(), 0o644); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			defer c.mu.Unlock()
			data, err := os.ReadFile(c.path)
			if err != nil {
				return
			}
			img, err := ParseImage(data)
			if err != nil {
				return
			}
			img.Flags &^= FlagMounted
			os.WriteFile(c.path, img.Bytes(), 0o644)
		})
	}, nil
}
