package appt

import "errors"

type Kind int

const (
	Dry Kind = iota
	Reefer
)

// Config 的时间字段单位均为分钟。
type Config struct {
	S, E, L, Wmax int64
	K             int
}

var (
	ErrInvalid   = errors.New("invalid argument")
	ErrClock     = errors.New("clock moved backwards")
	ErrNotFound  = errors.New("not found")
	ErrState     = errors.New("state conflict")
	ErrDuplicate = errors.New("duplicate reservation or dock")
	ErrCapacity  = errors.New("capacity full")
)

// Slot 是一辆车当前持有的有效预约。
type Slot struct {
	Kind Kind
	S    int64
}

type slotKey struct {
	s    int64
	kind Kind
}

type Bookings struct {
	cfg     Config
	lastNow int64
	active  map[string]Slot
	count   map[slotKey]int
}

func New(cfg Config) *Bookings { return &Bookings{cfg: cfg} }

func validID(id []byte) bool {
	return len(id) >= 1 && len(id) <= 32
}

// ValidID 供 dock/yard 复用同一编号规则。
func ValidID(id []byte) bool { return validID(id) }

func (b *Bookings) Book(truck []byte, kind Kind, s, now int64) error {
	if !validID(truck) || (kind != Dry && kind != Reefer) || s < 0 ||
		s%b.cfg.S != 0 || s < now {
		return ErrInvalid
	}
	if now < b.lastNow {
		return ErrClock
	}
	key := string(truck)
	if _, ok := b.active[key]; ok {
		return ErrDuplicate
	}
	sk := slotKey{s: s, kind: kind}
	if b.count[sk] >= b.cfg.K {
		return ErrCapacity
	}
	if b.active == nil {
		b.active = make(map[string]Slot)
		b.count = make(map[slotKey]int)
	}
	b.active[key] = Slot{Kind: kind, S: s}
	b.count[sk]++
	b.lastNow = now
	return nil
}

// Lookup 返回车辆当前是否持有有效预约。
func (b *Bookings) Lookup(truck []byte) (Slot, bool) {
	sl, ok := b.active[string(truck)]
	return sl, ok
}

// Consume 在签到时消耗预约（准点）。
func (b *Bookings) Consume(truck []byte) {
	delete(b.active, string(truck))
}

// Void 作废预约（早到/迟到降为候补），名额不退还。
func (b *Bookings) Void(truck []byte) {
	delete(b.active, string(truck))
}

// LastNow 暴露当前时钟，供组合者校验。
func (b *Bookings) LastNow() int64 { return b.lastNow }
