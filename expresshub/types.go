package expresshub

type Category uint8

const (
	CategoryNormal Category = iota + 1
	CategoryFragile
	CategoryLiquid
)

type Parcel struct {
	Waybill     string
	Destination string
	WeightGrams int64
	Category    Category
	AcceptedAt  int64
	BagID       int64
}

type AddParcelInput struct {
	Waybill     string
	Destination string
	WeightGrams int64
	Category    Category
	At          int64
}

type AddResult struct {
	SealedBagID int64
	OpenedBagID int64
	BagID       int64
}

type SealBagInput struct {
	Destination string
	At          int64
}

type SealResult struct {
	BagID int64
}

type DispatchBagInput struct {
	BagID     int64
	VehicleID string
	At        int64
}

type DispatchResult struct {
	BagID int64
}

type VerifyBagInput struct {
	BagID       int64
	Destination string
	Scanned     []string
	At          int64
}

type VerifyResult struct {
	BagID   int64
	Missing []string
	Extra   []string
}

type Config struct {
	MaxItems       int
	MaxWeightGrams int64
	DwellLimitSec  int64
}

type BagStatus uint8

const (
	BagStatusOpen BagStatus = iota + 1
	BagStatusSealed
	BagStatusDispatched
	BagStatusVerified
)

type BagView struct {
	ID             int64
	Destination    string
	Status         BagStatus
	MaxItems       int
	MaxWeightGrams int64
	DwellLimitSec  int64
	FirstAddedAt   int64
	SealedAt       int64
	DispatchedAt   int64
	VerifiedAt     int64
	VehicleID      string
	Items          []Parcel
	TotalWeight    int64
}

type LocationStatus uint8

const (
	LocationInOpenBag LocationStatus = iota + 1
	LocationInSealedBag
	LocationInDispatchedBag
	LocationPendingInvestigation
	LocationLeft
)

type ParcelLocation struct {
	Waybill     string
	BagID       int64
	Destination string
	Status      LocationStatus
}
