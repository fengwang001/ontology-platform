// Package escrow records per-session locked offers and maintains per-player
// aggregate locked totals so availability checks never scan other sessions.
package escrow

// Offer is a locked quote: item quantities plus gold.
type Offer struct {
	Items map[string]int64
	Gold  int64
}

// Escrow stores locks keyed by session and player. It is not goroutine-safe;
// callers must serialize access.
type Escrow struct {
	sessions map[int64]map[string]Offer  // sid -> player -> locked offer
	items    map[string]map[string]int64 // player -> item -> locked total
	gold     map[string]int64            // player -> locked gold total
	touched  int                         // session-lock records read/written
}

// New creates an empty escrow.
func New() *Escrow {
	return &Escrow{
		sessions: map[int64]map[string]Offer{},
		items:    map[string]map[string]int64{},
		gold:     map[string]int64{},
	}
}

// Replace atomically swaps the player's lock inside session sid and returns
// the previously locked offer (zero Offer if none). The items map is stored
// by reference; callers must not mutate it afterwards. touched grows by
// len(old.Items)+len(items)+2, independent of the player's other sessions.
func (e *Escrow) Replace(sid int64, player string, items map[string]int64, gold int64) Offer {
	players, ok := e.sessions[sid]
	if !ok {
		players = map[string]Offer{}
		e.sessions[sid] = players
	}
	old := players[player]
	e.touched += len(old.Items) + len(items) + 2
	for item, q := range old.Items {
		e.addLocked(player, item, -q)
	}
	e.gold[player] -= old.Gold
	for item, q := range items {
		e.addLocked(player, item, q)
	}
	e.gold[player] += gold
	if len(items) == 0 && gold == 0 {
		delete(players, player)
	} else {
		players[player] = Offer{Items: items, Gold: gold}
	}
	return old
}

// Release drops all locks of session sid.
func (e *Escrow) Release(sid int64) {
	players, ok := e.sessions[sid]
	if !ok {
		return
	}
	for player, off := range players {
		e.touched += len(off.Items) + 1
		for item, q := range off.Items {
			e.addLocked(player, item, -q)
		}
		e.gold[player] -= off.Gold
	}
	delete(e.sessions, sid)
}

// LockedQty returns the player's total locked quantity of item.
func (e *Escrow) LockedQty(player, item string) int64 {
	return e.items[player][item]
}

// LockedGold returns the player's total locked gold.
func (e *Escrow) LockedGold(player string) int64 { return e.gold[player] }

func (e *Escrow) addLocked(player, item string, delta int64) {
	locked, ok := e.items[player]
	if !ok {
		locked = map[string]int64{}
		e.items[player] = locked
	}
	next := locked[item] + delta
	if next == 0 {
		delete(locked, item)
	} else {
		locked[item] = next
	}
}
