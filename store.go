package store

import (
	"errors"

	"ontology/cond"
	"ontology/objstore"
	"ontology/quota"
)

var errInjected = errors.New("injected storage failure")

const (
	maxSize = int64(1_000_000_000_000)
	maxNow  = int64(1_000_000_000_000)
)

// Sentinel errors establish the reporting order: invalid argument >
// precondition failure > not found > quota exceeded > storage failure.
var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrVersioningImmutable = errors.New("versioning cannot be disabled")
	ErrNotFound            = errors.New("target not found")
	ErrQuotaExceeded       = errors.New("quota exceeded")
	ErrStorageFailure      = errors.New("storage failure")
)

// PreconditionFailedError reports which subcondition failed.
type PreconditionFailedError = cond.FailedConditionError

// PutResult reports the installed version, its etag and the applied net delta.
type PutResult struct {
	Version int64
	Etag    string
	Delta   int64
}

// DeleteResult reports the removed or appended version and the applied net
// delta (0 for an appended marker).
type DeleteResult struct {
	Version int64
	Marker  bool
	Delta   int64
}

type storageFailure struct{ cause error }

func (e *storageFailure) Error() string { return ErrStorageFailure.Error() + ": " + e.cause.Error() }
func (e *storageFailure) Unwrap() error { return e.cause }

// IsStorageFailure reports whether err is the injected storage failure.
func IsStorageFailure(err error) bool {
	var sf *storageFailure
	return errors.As(err, &sf)
}

// Store is the tenant object store orchestrating conditions, quota ledger and
// versioned objects.
type Store struct {
	objects      *objstore.Store
	ledger       *quota.Ledger
	afterReserve func() error
}

func NewStore() *Store {
	return &Store{
		objects:      objstore.NewStore(),
		ledger:       quota.NewLedger(),
		afterReserve: func() error { return nil },
	}
}

// SetAfterReserve installs the hook invoked after quota reservation and before
// object installation. A non-nil error rolls the reservation back.
func (s *Store) SetAfterReserve(f func() error) {
	if f == nil {
		f = func() error { return nil }
	}
	s.afterReserve = f
}

// SetQuota is always accepted, including q below current usage (over-quota).
func (s *Store) SetQuota(tenant string, q int64) error {
	if tenant == "" || q < 0 || q > quota.MaxQuota {
		return ErrInvalidArgument
	}
	s.ledger.SetQuota(tenant, q)
	return nil
}

// SetVersioning supports only the irreversible off -> on transition. Enabling
// an already enabled bucket is idempotent; disabling is rejected.
func (s *Store) SetVersioning(tenant string, on bool) error {
	if tenant == "" {
		return ErrInvalidArgument
	}
	s.objects.Lock(tenant)
	defer s.objects.Unlock(tenant)
	enabled := s.objects.Versioning(tenant)
	if on {
		if !enabled {
			s.objects.EnableVersioning(tenant)
		}
		return nil
	}
	if enabled {
		return ErrVersioningImmutable
	}
	return nil
}

func validIdentity(tenant, key string) bool {
	return tenant != "" && key != ""
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Put installs or overwrites an object subject to conditions and quota.
func (s *Store) Put(tenant, key string, size int64, etag string, c cond.Cond, now int64) (PutResult, error) {
	if !validIdentity(tenant, key) || size < 0 || size > maxSize || etag == "" ||
		!validNow(now) || c.Validate() != nil {
		return PutResult{}, ErrInvalidArgument
	}

	// Phase 1 (object lock): evaluate conditions against the current version
	// and reserve the tentative positive net delta.
	s.objects.Lock(tenant)
	versioned := s.objects.Versioning(tenant)
	if f := c.Evaluate(s.objects.View(tenant, key)); f != "" {
		s.objects.Unlock(tenant)
		return PutResult{}, &cond.FailedConditionError{Name: f}
	}
	oldSize, hasOld := s.objects.CurrentSize(tenant, key)
	d := putDelta(versioned, size, oldSize, hasOld)
	token, ok := s.ledger.Reserve(tenant, d)
	s.objects.Unlock(tenant)
	if !ok {
		return PutResult{}, ErrQuotaExceeded
	}

	// The reservation is now visible to other writers as pending usage while
	// the storage hook runs without the object lock.
	if err := s.afterReserve(); err != nil {
		token.Release()
		return PutResult{}, &storageFailure{cause: err}
	}

	// Phase 2 (object lock again): another writer may have committed, so
	// re-evaluate conditions and recompute the net delta before installing.
	s.objects.Lock(tenant)
	defer s.objects.Unlock(tenant)
	if f := c.Evaluate(s.objects.View(tenant, key)); f != "" {
		token.Release()
		return PutResult{}, &cond.FailedConditionError{Name: f}
	}
	versioned = s.objects.Versioning(tenant)
	oldSize, hasOld = s.objects.CurrentSize(tenant, key)
	d = putDelta(versioned, size, oldSize, hasOld)
	if !token.Adjust(d) {
		return PutResult{}, ErrQuotaExceeded
	}
	ver, _ := s.objects.InstallData(tenant, key, size, etag, now)
	token.Commit()
	return PutResult{Version: ver, Etag: etag, Delta: d}, nil
}

func putDelta(versioned bool, size int64, oldSize int64, hasOld bool) int64 {
	if versioned || !hasOld {
		return size
	}
	return size - oldSize
}

// Delete removes an object or version subject to conditions and quota.
func (s *Store) Delete(tenant, key string, ver int64, c cond.Cond, now int64) (DeleteResult, error) {
	if !validIdentity(tenant, key) || ver < -1 || !validNow(now) || c.Validate() != nil {
		return DeleteResult{}, ErrInvalidArgument
	}

	s.objects.Lock(tenant)
	versioned := s.objects.Versioning(tenant)
	if !versioned && ver != -1 {
		s.objects.Unlock(tenant)
		return DeleteResult{}, ErrInvalidArgument
	}
	if f := c.Evaluate(s.objects.View(tenant, key)); f != "" {
		s.objects.Unlock(tenant)
		return DeleteResult{}, &cond.FailedConditionError{Name: f}
	}

	var d int64
	if versioned {
		if ver != -1 {
			size, exists := s.objects.VersionSize(tenant, key, ver)
			if !exists {
				s.objects.Unlock(tenant)
				return DeleteResult{}, ErrNotFound
			}
			d = -size
		}
	} else {
		size, exists := s.objects.CurrentSize(tenant, key)
		if !exists {
			s.objects.Unlock(tenant)
			return DeleteResult{}, ErrNotFound
		}
		d = -size
	}
	token, _ := s.ledger.Reserve(tenant, d)
	s.objects.Unlock(tenant)

	if err := s.afterReserve(); err != nil {
		token.Release()
		return DeleteResult{}, &storageFailure{cause: err}
	}

	s.objects.Lock(tenant)
	defer s.objects.Unlock(tenant)

	// Versioning only ever goes off -> on. If that happens during the hook,
	// the serial-equivalent order is "enable then delete": an unspecified
	// delete appends a marker below; a specified delete follows the versioned
	// path, which is the only legal shape for this phase.
	versioned = s.objects.Versioning(tenant)
	if f := c.Evaluate(s.objects.View(tenant, key)); f != "" {
		token.Release()
		return DeleteResult{}, &cond.FailedConditionError{Name: f}
	}
	if versioned {
		if ver == -1 {
			// Append a delete marker; delta is 0 but a version is consumed.
			number := s.objects.InstallMarker(tenant, key, now)
			if !token.Adjust(0) {
				return DeleteResult{}, ErrQuotaExceeded
			}
			token.Commit()
			return DeleteResult{Version: number, Marker: true, Delta: 0}, nil
		}
		size, ok := s.objects.RemoveVersion(tenant, key, ver)
		if !ok {
			token.Release()
			return DeleteResult{}, ErrNotFound
		}
		d = -size
		if !token.Adjust(d) {
			return DeleteResult{}, ErrQuotaExceeded
		}
		token.Commit()
		return DeleteResult{Version: ver, Delta: d}, nil
	}
	size, ok := s.objects.RemoveUnique(tenant, key)
	if !ok {
		token.Release()
		return DeleteResult{}, ErrNotFound
	}
	d = -size
	if !token.Adjust(d) {
		return DeleteResult{}, ErrQuotaExceeded
	}
	token.Commit()
	return DeleteResult{Version: 0, Delta: d}, nil
}

// Quota reports the tenant quota (0 when unset).
func (s *Store) Quota(tenant string) int64 { return s.ledger.Quota(tenant) }

// Used reports committed stored bytes: the sum of every stored version.
func (s *Store) Used(tenant string) int64 { return s.ledger.CommittedUsed(tenant) }

// ObservedUsed reports usage plus outstanding positive reservations, i.e. what
// concurrent writers are charged against.
func (s *Store) ObservedUsed(tenant string) int64 { return s.ledger.Used(tenant) }

// Versioning reports the tenant versioning flag.
func (s *Store) Versioning(tenant string) bool { return s.objects.Versioning(tenant) }

// Current returns the current-version view of a key.
func (s *Store) Current(tenant, key string) cond.View { return s.objects.View(tenant, key) }

// VersionSize reports a specific version's size (markers are 0) and existence.
func (s *Store) VersionSize(tenant, key string, ver int64) (int64, bool) {
	return s.objects.VersionSize(tenant, key, ver)
}

// DecisionTouched returns version records touched by the latest current-version
// decision (bound: <= 2).
func (s *Store) DecisionTouched() int { return s.objects.DecisionTouched() }
