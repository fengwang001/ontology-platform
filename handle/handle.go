package handle

const (
	indexBits      = 32
	generationBits = 32
	indexMask      = 1<<indexBits - 1
	generationMask = 1<<generationBits - 1
	MaxIndex       = indexMask
	MaxGeneration  = generationMask
	MaxTableID     = uint64(1<<32 - 1)
)

// Handle is the opaque, generation-checked reference returned by a table.
type Handle struct {
	packed uint64
	table  uint64
}

// Encode creates a handle. Generation and table ID zero are reserved.
func Encode(tableID, slotIndex, generation uint64) Handle {
	if tableID == 0 || tableID > MaxTableID || slotIndex > MaxIndex ||
		generation == 0 || generation > MaxGeneration {
		return Handle{}
	}
	return Handle{
		packed: slotIndex | (generation << indexBits),
		table:  tableID,
	}
}

// Decode returns the table ID, slot index, and generation carried by h.
func (h Handle) Decode() (tableID, slotIndex, generation uint64) {
	return h.table, h.packed & indexMask, h.packed >> indexBits & generationMask
}

func (h Handle) IsZero() bool { return h == Handle{} }

// Equal reports whether two handles name exactly the same table generation.
func (h Handle) Equal(other Handle) bool { return h == other }
