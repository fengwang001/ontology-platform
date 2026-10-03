package ontology

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrClockSkew       = errors.New("ontology: clock skew")
	ErrTokenBlocked    = errors.New("ontology: token in feedback block window")
	ErrDeviceNotFound  = errors.New("ontology: device not found")
	ErrTokenUnowned    = errors.New("ontology: token has no owner binding")
)

type oldToken struct {
	tok       []byte
	retiredAt int64
}

type binding struct {
	user     []byte
	device   []byte
	cur      []byte
	lastSeen int64
	old      []oldToken
}

type tokenOwner struct {
	b     *binding
	isOld bool
}

// Registry is a push device token registry.
//
// All operations are safe for concurrent use; their effects are equivalent to
// some serial execution. A single RWMutex serializes every mutating call, and
// read-only Targets calls take a read lock.
type Registry struct {
	mu       sync.RWMutex
	d        int
	r        int
	g        int64
	b        int64
	maxNow   int64
	bindings map[string]*binding
	owners   map[string]tokenOwner
	// devices is the per-user binding index and also the unexported per-user
	// device counter: len(devices[userKey]) is always the number of bindings
	// currently owned by the user, independent of any other user's data.
	devices map[string]map[string]*binding
	blk     map[string]int64
}

// NewRegistry constructs a registry with the given parameters:
// deviceLimit D in [1,64], oldLimit R in [0,8], grace G in [0,1e9],
// blockFor B in [0,1e9].
func NewRegistry(deviceLimit, oldLimit int, grace, blockFor int64) (*Registry, error) {
	if deviceLimit < 1 || deviceLimit > 64 ||
		oldLimit < 0 || oldLimit > 8 ||
		grace < 0 || grace > 1_000_000_000 ||
		blockFor < 0 || blockFor > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Registry{
		d:        deviceLimit,
		r:        oldLimit,
		g:        grace,
		b:        blockFor,
		bindings: make(map[string]*binding),
		owners:   make(map[string]tokenOwner),
		devices:  make(map[string]map[string]*binding),
		blk:      make(map[string]int64),
	}, nil
}

// userKey and deviceKey are byte-exact (length-prefixed) composite keys.
func userKey(u []byte) string {
	return string(u)
}

func deviceKey(u, d []byte) string {
	key := make([]byte, 0, len(u)+1+len(d))
	key = append(key, u...)
	key = append(key, 0)
	key = append(key, d...)
	return string(key)
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func validEntity(v []byte, maxLen int) bool {
	return len(v) > 0 && len(v) <= maxLen
}

// releaseBinding deletes a whole binding, releasing every token it owns
// (current plus old tokens) and decrementing the per-user device counter.
func (reg *Registry) releaseBinding(b *binding) {
	delete(reg.owners, string(b.cur))
	for _, ot := range b.old {
		delete(reg.owners, string(ot.tok))
	}
	uk := userKey(b.user)
	delete(reg.devices[uk], string(b.device))
	if len(reg.devices[uk]) == 0 {
		delete(reg.devices, uk)
	}
	delete(reg.bindings, deviceKey(b.user, b.device))
}

// Register binds token to (user, device) at time now.
func (reg *Registry) Register(user, device, token []byte, now int64) error {
	if !validEntity(user, 64) || !validEntity(device, 64) || !validEntity(token, 256) || !validNow(now) {
		return ErrInvalidArgument
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if now < reg.maxNow {
		return ErrClockSkew
	}
	tk := string(token)
	if until, ok := reg.blk[tk]; ok && until > now {
		return ErrTokenBlocked
	}

	uk := userKey(user)
	dk := deviceKey(user, device)

	// Step (1): release the token from any prior ownership.
	if owner, owned := reg.owners[tk]; owned {
		y := owner.b
		sameBinding := string(y.user) == string(user) && string(y.device) == string(device)
		if !owner.isOld {
			if sameBinding {
				// Re-registration of the current token only refreshes lastSeen.
				y.lastSeen = now
				reg.maxNow = now
				return nil
			}
			// Token is another binding's current token: delete that whole
			// binding, including all of its old tokens.
			reg.releaseBinding(y)
		} else {
			// Token is an old token (possibly of (user, device) itself):
			// remove only that old-token entry.
			for i, ot := range y.old {
				if string(ot.tok) == tk {
					reg.removeOldToken(y, i)
					break
				}
			}
		}
	}

	if b, ok := reg.bindings[dk]; ok {
		// Step (2): existing device. Retire its current token into the head
		// of the old-token list, capped at R; the dropped oldest entry is
		// released.
		retired := oldToken{tok: append([]byte(nil), b.cur...), retiredAt: now}
		b.old = append([]oldToken{retired}, b.old...)
		for len(b.old) > reg.r {
			last := b.old[len(b.old)-1]
			delete(reg.owners, string(last.tok))
			b.old = b.old[:len(b.old)-1]
		}
		if len(b.old) > 0 {
			reg.owners[string(b.old[0].tok)] = tokenOwner{b: b, isOld: true}
		}
		b.cur = append([]byte(nil), token...)
		b.lastSeen = now
		reg.owners[tk] = tokenOwner{b: b, isOld: false}
		reg.maxNow = now
		return nil
	}

	// Step (3): new device. Evict the device with the smallest lastSeen
	// (ties: smallest device name in byte order) if the user is already at
	// the limit; the eviction happens after step (1), so stealing the current
	// token of another device of the same user frees one slot first.
	if len(reg.devices[uk]) >= reg.d {
		var victim *binding
		for _, b := range reg.devices[uk] {
			if victim == nil ||
				b.lastSeen < victim.lastSeen ||
				(b.lastSeen == victim.lastSeen && bytes.Compare(b.device, victim.device) < 0) {
				victim = b
			}
		}
		reg.releaseBinding(victim)
	}

	b := &binding{
		user:     append([]byte(nil), user...),
		device:   append([]byte(nil), device...),
		cur:      append([]byte(nil), token...),
		lastSeen: now,
	}
	reg.bindings[dk] = b
	reg.owners[tk] = tokenOwner{b: b, isOld: false}
	if reg.devices[uk] == nil {
		reg.devices[uk] = make(map[string]*binding)
	}
	reg.devices[uk][string(device)] = b
	reg.maxNow = now
	return nil
}

// removeOldToken removes the old-token entry at index i and releases ownership.
func (reg *Registry) removeOldToken(b *binding, i int) {
	delete(reg.owners, string(b.old[i].tok))
	copy(b.old[i:], b.old[i+1:])
	b.old = b.old[:len(b.old)-1]
}

// Feedback records a provider invalidation report for token.
func (reg *Registry) Feedback(token []byte, now int64) error {
	if !validEntity(token, 256) || !validNow(now) {
		return ErrInvalidArgument
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if now < reg.maxNow {
		return ErrClockSkew
	}
	tk := string(token)
	owner, owned := reg.owners[tk]
	if !owned {
		return ErrTokenUnowned
	}
	if !owner.isOld {
		reg.releaseBinding(owner.b)
	} else {
		for i, ot := range owner.b.old {
			if string(ot.tok) == tk {
				reg.removeOldToken(owner.b, i)
				break
			}
		}
	}
	reg.blk[tk] = now + reg.b
	reg.maxNow = now
	return nil
}

// Touch refreshes lastSeen of an existing binding.
func (reg *Registry) Touch(user, device []byte, now int64) error {
	if !validEntity(user, 64) || !validEntity(device, 64) || !validNow(now) {
		return ErrInvalidArgument
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if now < reg.maxNow {
		return ErrClockSkew
	}
	b, ok := reg.bindings[deviceKey(user, device)]
	if !ok {
		return ErrDeviceNotFound
	}
	b.lastSeen = now
	reg.maxNow = now
	return nil
}

// Unregister deletes a binding and releases all of its tokens.
// Released tokens do not enter the block table.
func (reg *Registry) Unregister(user, device []byte, now int64) error {
	if !validEntity(user, 64) || !validEntity(device, 64) || !validNow(now) {
		return ErrInvalidArgument
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if now < reg.maxNow {
		return ErrClockSkew
	}
	b, ok := reg.bindings[deviceKey(user, device)]
	if !ok {
		return ErrDeviceNotFound
	}
	reg.releaseBinding(b)
	reg.maxNow = now
	return nil
}

// Targets returns the delivery targets for user at time now: current tokens
// ordered by lastSeen desc / device name asc, then in-grace old tokens
// ordered by retiredAt desc / token asc.
func (reg *Registry) Targets(user []byte, now int64) ([][]byte, error) {
	if !validEntity(user, 64) || !validNow(now) {
		return nil, ErrInvalidArgument
	}
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	if now < reg.maxNow {
		return nil, ErrClockSkew
	}

	// Only this user's bindings are examined (per-user index), so the cost
	// depends solely on the user's device count and old-token count.
	var bs []*binding
	for _, b := range reg.devices[string(user)] {
		bs = append(bs, b)
	}
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].lastSeen != bs[j].lastSeen {
			return bs[i].lastSeen > bs[j].lastSeen
		}
		return bytes.Compare(bs[i].device, bs[j].device) < 0
	})

	type graceToken struct {
		tok       []byte
		retiredAt int64
	}
	var old []graceToken
	out := make([][]byte, 0)
	for _, b := range bs {
		out = append(out, append([]byte(nil), b.cur...))
		for _, ot := range b.old {
			if now < ot.retiredAt+reg.g {
				old = append(old, graceToken{tok: ot.tok, retiredAt: ot.retiredAt})
			}
		}
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].retiredAt != old[j].retiredAt {
			return old[i].retiredAt > old[j].retiredAt
		}
		return bytes.Compare(old[i].tok, old[j].tok) < 0
	})
	for _, ot := range old {
		out = append(out, append([]byte(nil), ot.tok...))
	}
	return out, nil
}
