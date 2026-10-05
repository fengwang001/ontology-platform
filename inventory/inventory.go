// Package inventory tracks per-player item holdings and gold, enforcing the
// slot (item-kind) limit and the gold cap on grants.
package inventory

import "errors"

var (
	ErrParam     = errors.New("inventory: invalid parameter")
	ErrSlotLimit = errors.New("inventory: slot limit exceeded")
	ErrGoldCap   = errors.New("inventory: gold cap exceeded")
)

// maxGrant bounds a single grant so holdings arithmetic never overflows.
const maxGrant = int64(1_000_000_000_000)

type account struct {
	gold  int64
	items map[string]int64
}

// Inventory holds all players' assets. It is not goroutine-safe; callers
// must serialize access.
type Inventory struct {
	goldCap int64
	slots   int64
	burned  int64
	players map[string]*account
}

// New creates an empty inventory with the given gold cap and slot limit.
func New(goldCap, slots int64) *Inventory {
	return &Inventory{goldCap: goldCap, slots: slots, players: map[string]*account{}}
}

// Has reports whether the player is registered.
func (inv *Inventory) Has(player string) bool {
	_, ok := inv.players[player]
	return ok
}

// Grant adds qty of item to the player, registering the player. It fails
// with ErrSlotLimit if the player would exceed the item-kind limit.
func (inv *Inventory) Grant(player, item string, qty int64) error {
	if player == "" || item == "" || qty < 1 || qty > maxGrant {
		return ErrParam
	}
	acc, ok := inv.players[player]
	if !ok {
		acc = &account{items: map[string]int64{}}
	}
	if acc.items[item] == 0 && int64(len(acc.items)) >= inv.slots {
		return ErrSlotLimit
	}
	acc.items[item] += qty
	inv.players[player] = acc
	return nil
}

// GrantGold adds g gold to the player, registering the player. It fails
// with ErrGoldCap if the player would exceed the gold cap.
func (inv *Inventory) GrantGold(player string, g int64) error {
	if player == "" || g < 0 || g > maxGrant {
		return ErrParam
	}
	acc, ok := inv.players[player]
	if !ok {
		acc = &account{items: map[string]int64{}}
	}
	if acc.gold+g > inv.goldCap {
		return ErrGoldCap
	}
	acc.gold += g
	inv.players[player] = acc
	return nil
}

// Gold returns the player's gold balance.
func (inv *Inventory) Gold(player string) int64 {
	if acc, ok := inv.players[player]; ok {
		return acc.gold
	}
	return 0
}

// Qty returns the player's held quantity of item.
func (inv *Inventory) Qty(player, item string) int64 {
	if acc, ok := inv.players[player]; ok {
		return acc.items[item]
	}
	return 0
}

// Kinds returns the number of item kinds with positive quantity.
func (inv *Inventory) Kinds(player string) int64 {
	if acc, ok := inv.players[player]; ok {
		return int64(len(acc.items))
	}
	return 0
}

// AddItem adjusts a holding by delta (negative to remove). Callers must
// guarantee the result stays non-negative.
func (inv *Inventory) AddItem(player, item string, delta int64) {
	acc := inv.players[player]
	if acc == nil {
		return
	}
	next := acc.items[item] + delta
	if next <= 0 {
		delete(acc.items, item)
	} else {
		acc.items[item] = next
	}
}

// AddGold adjusts a gold balance by delta (negative to remove).
func (inv *Inventory) AddGold(player string, delta int64) {
	if acc, ok := inv.players[player]; ok {
		acc.gold += delta
	}
}

// Burn adds g to the burned gold counter.
func (inv *Inventory) Burn(g int64) { inv.burned += g }

// Burned returns the total burned gold.
func (inv *Inventory) Burned() int64 { return inv.burned }

// GoldCap returns the configured gold cap.
func (inv *Inventory) GoldCap() int64 { return inv.goldCap }

// Slots returns the configured slot limit.
func (inv *Inventory) Slots() int64 { return inv.slots }
