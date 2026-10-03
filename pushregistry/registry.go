package pushregistry

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrTokenBlocked    = errors.New("token is blocked")
	ErrDeviceNotFound  = errors.New("device not found")
	ErrTokenNotFound   = errors.New("token not found")
)

type oldToken struct {
	token     string
	retiredAt int64
}

type binding struct {
	user     string
	device   string
	cur      string
	lastSeen int64
	old      []oldToken
}

type userIndex struct {
	bindings map[string]*binding
}

type Registry struct {
	mu             sync.Mutex
	deviceLimit    int
	retainedTokens int
	gracePeriod    int64
	blockDuration  int64
	maxNow         int64
	users          map[string]*userIndex
	tokenOwners    map[string]tokenOwner
	blocks         map[string]int64
	targetLookups  map[string]int64
}

type tokenOwner struct {
	binding *binding
	current bool
}

func NewRegistry(deviceLimit, retainedTokens int, gracePeriod, blockDuration int64) (*Registry, error) {
	if deviceLimit < 1 || deviceLimit > 64 ||
		retainedTokens < 0 || retainedTokens > 8 ||
		gracePeriod < 0 || gracePeriod > 1_000_000_000 ||
		blockDuration < 0 || blockDuration > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}

	return &Registry{
		deviceLimit:    deviceLimit,
		retainedTokens: retainedTokens,
		gracePeriod:    gracePeriod,
		blockDuration:  blockDuration,
		users:          make(map[string]*userIndex),
		tokenOwners:    make(map[string]tokenOwner),
		blocks:         make(map[string]int64),
		targetLookups:  make(map[string]int64),
	}, nil
}

func (r *Registry) Register(user, device, token []byte, now int64) error {
	us, ds, ts, err := r.validateChange(user, device, token, now)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockRollback
	}
	if r.blocks[ts] > now {
		return ErrTokenBlocked
	}

	if owner, ok := r.tokenOwners[ts]; ok {
		switch {
		case owner.current && owner.binding.user == us && owner.binding.device == ds:
			owner.binding.lastSeen = now
			r.maxNow = now
			return nil
		case owner.current:
			r.deleteBinding(owner.binding)
		default:
			r.removeOldToken(owner.binding, ts)
		}
	}

	idx := r.users[us]
	if idx == nil {
		idx = &userIndex{bindings: make(map[string]*binding)}
		r.users[us] = idx
	}

	if target := idx.bindings[ds]; target != nil {
		if target.cur != ts {
			r.releaseToken(target.cur)
			r.insertOldToken(target, oldToken{token: target.cur, retiredAt: now})
			target.cur = ts
			r.tokenOwners[ts] = tokenOwner{binding: target, current: true}
		}
		target.lastSeen = now
	} else {
		if len(idx.bindings) >= r.deviceLimit {
			r.evictOldest(idx)
			idx = r.users[us]
			if idx == nil {
				idx = &userIndex{bindings: make(map[string]*binding)}
				r.users[us] = idx
			}
		}
		target = &binding{
			user:     us,
			device:   ds,
			cur:      ts,
			lastSeen: now,
			old:      make([]oldToken, 0, r.retainedTokens),
		}
		idx.bindings[ds] = target
		r.tokenOwners[ts] = tokenOwner{binding: target, current: true}
	}

	r.maxNow = now
	return nil
}

func (r *Registry) Feedback(token []byte, now int64) error {
	ts, err := r.validateClockToken(token, now)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockRollback
	}
	owner, ok := r.tokenOwners[ts]
	if !ok {
		return ErrTokenNotFound
	}

	if owner.current {
		r.deleteBinding(owner.binding)
	} else {
		r.removeOldToken(owner.binding, ts)
	}
	r.blocks[ts] = now + r.blockDuration
	r.maxNow = now
	return nil
}

func (r *Registry) Touch(user, device []byte, now int64) error {
	us, ds, err := r.validateClockDevice(user, device, now)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockRollback
	}
	bound := r.findBinding(us, ds)
	if bound == nil {
		return ErrDeviceNotFound
	}
	bound.lastSeen = now
	r.maxNow = now
	return nil
}

func (r *Registry) Unregister(user, device []byte, now int64) error {
	us, ds, err := r.validateClockDevice(user, device, now)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return ErrClockRollback
	}
	bound := r.findBinding(us, ds)
	if bound == nil {
		return ErrDeviceNotFound
	}
	r.deleteBinding(bound)
	r.maxNow = now
	return nil
}

func (r *Registry) Targets(user []byte, now int64) ([][]byte, error) {
	if err := validateName(user, 64); err != nil {
		return nil, err
	}
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.maxNow {
		return nil, ErrClockRollback
	}
	us := string(user)
	idx := r.users[us]

	var current []binding
	var old []oldToken
	if idx != nil {
		current = make([]binding, 0, len(idx.bindings))
		oldCount := 0
		for _, bound := range idx.bindings {
			current = append(current, *bound)
			oldCount += len(bound.old)
			old = append(old, bound.old...)
		}
		r.targetLookups[us] += int64(len(idx.bindings) + oldCount)
	}

	sort.Slice(current, func(i, j int) bool {
		if current[i].lastSeen != current[j].lastSeen {
			return current[i].lastSeen > current[j].lastSeen
		}
		return current[i].device < current[j].device
	})
	sort.Slice(old, func(i, j int) bool {
		if old[i].retiredAt != old[j].retiredAt {
			return old[i].retiredAt > old[j].retiredAt
		}
		return old[i].token < old[j].token
	})

	result := make([][]byte, 0, len(current)+len(old))
	for _, bound := range current {
		result = append(result, []byte(bound.cur))
	}
	for _, item := range old {
		if now < item.retiredAt+r.gracePeriod {
			result = append(result, []byte(item.token))
		}
	}
	return result, nil
}

func (r *Registry) validateChange(user, device, token []byte, now int64) (string, string, string, error) {
	if err := validateName(user, 64); err != nil {
		return "", "", "", err
	}
	if err := validateName(device, 64); err != nil {
		return "", "", "", err
	}
	if err := validateName(token, 256); err != nil {
		return "", "", "", err
	}
	if !validNow(now) {
		return "", "", "", ErrInvalidArgument
	}
	return string(user), string(device), string(token), nil
}

func (r *Registry) validateClockToken(token []byte, now int64) (string, error) {
	if err := validateName(token, 256); err != nil {
		return "", err
	}
	if !validNow(now) {
		return "", ErrInvalidArgument
	}
	return string(token), nil
}

func (r *Registry) validateClockDevice(user, device []byte, now int64) (string, string, error) {
	if err := validateName(user, 64); err != nil {
		return "", "", err
	}
	if err := validateName(device, 64); err != nil {
		return "", "", err
	}
	if !validNow(now) {
		return "", "", ErrInvalidArgument
	}
	return string(user), string(device), nil
}

func validateName(value []byte, maxLength int) error {
	if len(value) == 0 || len(value) > maxLength {
		return ErrInvalidArgument
	}
	return nil
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func (r *Registry) findBinding(user, device string) *binding {
	idx := r.users[user]
	if idx == nil {
		return nil
	}
	return idx.bindings[device]
}

func (r *Registry) evictOldest(idx *userIndex) {
	var victim *binding
	for _, bound := range idx.bindings {
		if victim == nil ||
			bound.lastSeen < victim.lastSeen ||
			(bound.lastSeen == victim.lastSeen && bound.device < victim.device) {
			victim = bound
		}
	}
	if victim != nil {
		r.deleteBinding(victim)
	}
}

func (r *Registry) deleteBinding(bound *binding) {
	r.releaseToken(bound.cur)
	for _, item := range bound.old {
		r.releaseToken(item.token)
	}

	idx := r.users[bound.user]
	delete(idx.bindings, bound.device)
	if len(idx.bindings) == 0 {
		delete(r.users, bound.user)
	}
}

func (r *Registry) insertOldToken(bound *binding, item oldToken) {
	bound.old = append([]oldToken{item}, bound.old...)
	if len(bound.old) > r.retainedTokens {
		released := bound.old[r.retainedTokens:]
		bound.old = bound.old[:r.retainedTokens]
		for _, oldItem := range released {
			r.releaseToken(oldItem.token)
		}
	}
	if len(bound.old) > 0 && bound.old[0] == item {
		r.tokenOwners[item.token] = tokenOwner{binding: bound, current: false}
	}
}

func (r *Registry) removeOldToken(bound *binding, token string) {
	for i, item := range bound.old {
		if item.token == token {
			bound.old = append(bound.old[:i], bound.old[i+1:]...)
			r.releaseToken(token)
			return
		}
	}
}

func (r *Registry) releaseToken(token string) {
	delete(r.tokenOwners, token)
}
