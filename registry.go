package baggage

import "sync"

// Registry 机场、承运人、订座记录与旅客的登记表。
// 所有读取均为 map 直查，O(1)，与系统内机场总数、历史记录数无关。
type Registry struct {
	mu        sync.RWMutex
	airports  map[string]Airport
	carriers  map[string]Carrier
	pnrs      map[string]map[string]Tier // pnr -> (旅客 -> 会员等级)
	passenger map[string]string          // 旅客 -> pnr
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{
		airports:  make(map[string]Airport),
		carriers:  make(map[string]Carrier),
		pnrs:      make(map[string]map[string]Tier),
		passenger: make(map[string]string),
	}
}

// AddAirport 登记机场，编号重复或为空时返回 ErrInvalid。
func (r *Registry) AddAirport(a Airport) error {
	if a.Code == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.airports[a.Code]; ok {
		return ErrInvalid
	}
	r.airports[a.Code] = a
	return nil
}

// AddCarrier 登记承运人，编号重复、为空或制式非法时返回 ErrInvalid。
func (r *Registry) AddCarrier(c Carrier) error {
	if c.ID == "" || (c.Mode != ModePiece && c.Mode != ModeWeight) {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.carriers[c.ID]; ok {
		return ErrInvalid
	}
	r.carriers[c.ID] = c
	return nil
}

// RegisterPNR 登记一条订座记录及其旅客名单（含各自会员等级）。
func (r *Registry) RegisterPNR(pnr string, passengers []Passenger) error {
	if pnr == "" || len(passengers) == 0 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pnrs[pnr]; ok {
		return ErrInvalid
	}
	for _, p := range passengers {
		if p.ID == "" {
			return ErrInvalid
		}
		if _, taken := r.passenger[p.ID]; taken {
			return ErrInvalid
		}
	}
	members := make(map[string]Tier, len(passengers))
	for _, p := range passengers {
		members[p.ID] = p.Tier
		r.passenger[p.ID] = pnr
	}
	r.pnrs[pnr] = members
	return nil
}

func (r *Registry) airport(code string) (Airport, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.airports[code]
	return a, ok
}

func (r *Registry) carrier(id string) (Carrier, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.carriers[id]
	return c, ok
}

func (r *Registry) pnrPassengers(pnr string) (map[string]Tier, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.pnrs[pnr]
	if !ok {
		return nil, false
	}
	out := make(map[string]Tier, len(m))
	for id, tier := range m {
		out[id] = tier
	}
	return out, true
}

func (r *Registry) passengerPNR(passengerID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.passenger[passengerID]
	return p, ok
}

// Passenger 旅客信息。
type Passenger struct {
	ID   string
	Tier Tier
}
