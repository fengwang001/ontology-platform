package fsck

import (
	"fmt"
	"os"
	"sync"
)

// Volume serializes access to one on-disk image: any number of concurrent
// read-only checks are allowed, while repair and write mounts are mutually
// exclusive.
type Volume struct {
	mu      sync.RWMutex
	path    string
	mounted bool
}

// OpenVolume opens the image at path.
func OpenVolume(path string) *Volume {
	return &Volume{path: path}
}

// Check runs a read-only consistency check. Safe for concurrent use.
func (v *Volume) Check() (*Report, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	img, err := readImageFile(v.path)
	if err != nil {
		return nil, err
	}
	return Check(img)
}

// Repair checks and repairs the image, atomically replacing it on success.
// The returned report describes the findings on the pre-repair image.
// On any error the original image is left byte-identical.
func (v *Volume) Repair(opts RepairOptions) (*Report, *RepairLog, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.mounted {
		return nil, nil, ErrMounted
	}
	img, err := readImageFile(v.path)
	if err != nil {
		return nil, nil, err
	}
	report, err := Check(img)
	if err != nil {
		return nil, nil, err
	}
	log, err := Repair(img)
	if err != nil {
		return nil, nil, err
	}
	if err := writeImageFileAtomic(v.path, img, opts.FailAt); err != nil {
		return nil, nil, err
	}
	return report, log, nil
}

// Mount is an exclusive write mount handle.
type Mount struct {
	v      *Volume
	closed bool
}

// MountWrite acquires an exclusive write mount on the volume.
func (v *Volume) MountWrite() (*Mount, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.mounted {
		return nil, ErrMounted
	}
	v.mounted = true
	return &Mount{v: v}, nil
}

// Close releases the write mount.
func (m *Mount) Close() {
	m.v.mu.Lock()
	defer m.v.mu.Unlock()
	if !m.closed {
		m.v.mounted = false
		m.closed = true
	}
}

func readImageFile(path string) (*Image, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// writeImageFileAtomic serializes img to a temporary file next to path and
// atomically renames it over path. failAt injects a failure at "mid-write"
// or "before-rename"; in every failure mode path is left untouched and the
// temporary file is removed.
func writeImageFileAtomic(path string, img *Image, failAt string) error {
	raw := img.Serialize()
	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	defer os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if failAt == "mid-write" {
		if _, err := f.Write(raw[:len(raw)/2]); err != nil {
			f.Close()
			return err
		}
		f.Close()
		return ErrInjected
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if failAt == "before-rename" {
		return ErrInjected
	}
	return os.Rename(tmp, path)
}
