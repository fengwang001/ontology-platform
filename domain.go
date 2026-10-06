package ontology

import "sync"

type Room struct {
	ID   int
	Area int
}

type Segment struct {
	RoomID int
	Start  int
	End    int
}

type Tenant struct {
	ID       int
	Segments []Segment
}

type Bill struct {
	ID          int
	Amount      int
	Start       int
	End         int
	PayerID     int
	Landlord    bool
	ByArea      bool
	CreatedAt   int
	FinalAmount int
	Disputed    bool
	Adjudicated bool
}

type Share struct {
	TenantID int
	Day      int
	Amount   int
	Landlord bool
}

type Edge struct {
	FromID int
	ToID   int
	Amount int
}

type Settlement struct {
	TenantID   int
	Day        int
	CreatedAt  int
	Supplement bool
	Edges      []Edge
}

type edgeKey struct {
	a int
	b int
}

type settlementState struct {
	day       int
	createdAt int
	baseline  map[edgeKey]int
	seen      map[int]int
}

type Service struct {
	mu            sync.RWMutex
	rooms         map[int]Room
	tenants       map[int]*Tenant
	bills         map[int]*Bill
	allocations   map[int][]Share
	removedShares map[int][]Share
	net           map[edgeKey]int
	settlements   map[int][]*Settlement
	settled       map[int]*settlementState
	version       int
	lastNow       int
	disputeWindow int
	nextTenantID  int
	nextBillID    int
}

func NewService(rooms []Room, initialNow int, disputeWindow int) *Service {
	service := &Service{
		rooms:         make(map[int]Room),
		tenants:       make(map[int]*Tenant),
		bills:         make(map[int]*Bill),
		allocations:   make(map[int][]Share),
		removedShares: make(map[int][]Share),
		net:           make(map[edgeKey]int),
		settlements:   make(map[int][]*Settlement),
		settled:       make(map[int]*settlementState),
		lastNow:       initialNow,
		disputeWindow: disputeWindow,
	}
	for _, room := range rooms {
		service.rooms[room.ID] = room
	}
	return service
}
