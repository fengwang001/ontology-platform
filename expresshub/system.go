package expresshub

import (
	"sort"
	"sync"
)

type activeParcel struct {
	parcel  Parcel
	bagID   int64
	pending bool
}

type bag struct {
	view BagView
}

type System struct {
	mu            sync.RWMutex
	cfg           Config
	lastTime      int64
	nextBagID     int64
	openByStation map[string]int64
	bags          map[int64]*bag
	active        map[string]activeParcel
}

func New(cfg Config) (*System, error) {
	if cfg.MaxItems <= 0 {
		return nil, &Error{Code: InvalidArgument, Msg: "max items must be positive"}
	}
	if cfg.MaxWeightGrams <= 0 {
		return nil, &Error{Code: InvalidArgument, Msg: "max weight must be positive"}
	}
	if cfg.DwellLimitSec < 0 {
		return nil, &Error{Code: InvalidArgument, Msg: "dwell limit must not be negative"}
	}
	return &System{
		cfg:           cfg,
		nextBagID:     1,
		openByStation: make(map[string]int64),
		bags:          make(map[int64]*bag),
		active:        make(map[string]activeParcel),
	}, nil
}

func (s *System) AddParcel(input AddParcelInput) (AddResult, error) {
	if err := validateParcelInput(input); err != nil {
		return AddResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.At); err != nil {
		return AddResult{}, err
	}
	if entry, exists := s.active[input.Waybill]; exists {
		if entry.pending {
			return AddResult{}, errorf(BusinessReject, "waybill %s is pending investigation", input.Waybill)
		}
		return AddResult{}, errorf(BusinessReject, "waybill %s already on site", input.Waybill)
	}
	if input.WeightGrams > s.cfg.MaxWeightGrams {
		return AddResult{}, errorf(BusinessReject, "waybill %s exceeds bag weight limit", input.Waybill)
	}

	var sealedBagID int64
	var openedBagID int64
	openID := s.openByStation[input.Destination]
	if openID != 0 && s.mustSealBeforeAdd(s.bags[openID], input) {
		s.sealOpenBag(input.Destination, input.At)
		sealedBagID = openID
		openID = 0
	}

	if openID == 0 {
		openedBagID = s.openBag(input.Destination, input.At)
		openID = openedBagID
	}

	target := s.bags[openID]
	parcel := Parcel{
		Waybill:     input.Waybill,
		Destination: input.Destination,
		WeightGrams: input.WeightGrams,
		Category:    input.Category,
		AcceptedAt:  input.At,
		BagID:       openID,
	}
	target.view.Items = append(target.view.Items, parcel)
	target.view.TotalWeight += input.WeightGrams
	if target.view.FirstAddedAt == 0 && len(target.view.Items) == 1 {
		target.view.FirstAddedAt = input.At
	}
	s.active[input.Waybill] = activeParcel{parcel: parcel, bagID: openID}
	s.lastTime = input.At

	return AddResult{SealedBagID: sealedBagID, OpenedBagID: openedBagID, BagID: openID}, nil
}

func (s *System) SealBag(input SealBagInput) (SealResult, error) {
	if input.Destination == "" {
		return SealResult{}, errorf(InvalidArgument, "destination is required")
	}
	if input.At < 0 {
		return SealResult{}, errorf(InvalidArgument, "time must not be negative")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.At); err != nil {
		return SealResult{}, err
	}
	bagID := s.openByStation[input.Destination]
	if bagID == 0 {
		return SealResult{}, errorf(InvalidState, "destination %s has no open bag", input.Destination)
	}
	s.sealOpenBag(input.Destination, input.At)
	s.lastTime = input.At
	return SealResult{BagID: bagID}, nil
}

func (s *System) DispatchBag(input DispatchBagInput) (DispatchResult, error) {
	if input.BagID <= 0 {
		return DispatchResult{}, errorf(InvalidArgument, "bag id must be positive")
	}
	if input.VehicleID == "" {
		return DispatchResult{}, errorf(InvalidArgument, "vehicle id is required")
	}
	if input.At < 0 {
		return DispatchResult{}, errorf(InvalidArgument, "time must not be negative")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.At); err != nil {
		return DispatchResult{}, err
	}
	target := s.bags[input.BagID]
	if target == nil {
		return DispatchResult{}, errorf(NotFound, "bag %d not found", input.BagID)
	}
	if target.view.Status != BagStatusSealed {
		return DispatchResult{}, errorf(InvalidState, "bag %d is not sealed", input.BagID)
	}
	target.view.Status = BagStatusDispatched
	target.view.VehicleID = input.VehicleID
	target.view.DispatchedAt = input.At
	s.lastTime = input.At
	return DispatchResult{BagID: input.BagID}, nil
}

func (s *System) VerifyBag(input VerifyBagInput) (VerifyResult, error) {
	if input.BagID <= 0 {
		return VerifyResult{}, errorf(InvalidArgument, "bag id must be positive")
	}
	if input.Destination == "" {
		return VerifyResult{}, errorf(InvalidArgument, "destination is required")
	}
	if input.At < 0 {
		return VerifyResult{}, errorf(InvalidArgument, "time must not be negative")
	}
	if hasDuplicate(input.Scanned) {
		return VerifyResult{}, errorf(InvalidArgument, "scanned waybills contain duplicates")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.At); err != nil {
		return VerifyResult{}, err
	}
	target := s.bags[input.BagID]
	if target == nil {
		return VerifyResult{}, errorf(NotFound, "bag %d not found", input.BagID)
	}
	if target.view.Status != BagStatusDispatched {
		return VerifyResult{}, errorf(InvalidState, "bag %d is not dispatched", input.BagID)
	}
	if target.view.Destination != input.Destination {
		return VerifyResult{}, errorf(StationMismatch, "bag destination %s does not match %s", target.view.Destination, input.Destination)
	}

	scannedSet := make(map[string]struct{}, len(input.Scanned))
	for _, waybill := range input.Scanned {
		scannedSet[waybill] = struct{}{}
	}
	missing := make([]string, 0)
	extra := make([]string, 0)
	for _, item := range target.view.Items {
		if _, ok := scannedSet[item.Waybill]; !ok {
			missing = append(missing, item.Waybill)
		}
	}
	itemSet := make(map[string]struct{}, len(target.view.Items))
	for _, item := range target.view.Items {
		itemSet[item.Waybill] = struct{}{}
	}
	for _, waybill := range input.Scanned {
		if _, ok := itemSet[waybill]; !ok {
			extra = append(extra, waybill)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	for _, item := range target.view.Items {
		if _, ok := scannedSet[item.Waybill]; ok {
			delete(s.active, item.Waybill)
			continue
		}
		entry := s.active[item.Waybill]
		entry.pending = true
		entry.bagID = target.view.ID
		s.active[item.Waybill] = entry
	}
	target.view.Status = BagStatusVerified
	target.view.VerifiedAt = input.At
	s.lastTime = input.At

	return VerifyResult{BagID: input.BagID, Missing: missing, Extra: extra}, nil
}

func validateParcelInput(input AddParcelInput) error {
	if input.Waybill == "" {
		return errorf(InvalidArgument, "waybill is required")
	}
	if input.Destination == "" {
		return errorf(InvalidArgument, "destination is required")
	}
	if input.WeightGrams < 1 || input.WeightGrams > 10_000_000 {
		return errorf(InvalidArgument, "weight grams must be in [1, 10000000]")
	}
	switch input.Category {
	case CategoryNormal, CategoryFragile, CategoryLiquid:
	default:
		return errorf(InvalidArgument, "category is invalid")
	}
	if input.At < 0 {
		return errorf(InvalidArgument, "time must not be negative")
	}
	return nil
}

func (s *System) checkClock(at int64) error {
	if at < s.lastTime {
		return errorf(ClockRewound, "time %d is before last accepted time %d", at, s.lastTime)
	}
	return nil
}

func (s *System) mustSealBeforeAdd(target *bag, input AddParcelInput) bool {
	if len(target.view.Items) >= s.cfg.MaxItems {
		return true
	}
	if target.view.TotalWeight+input.WeightGrams > s.cfg.MaxWeightGrams {
		return true
	}
	if categoriesConflict(target, input.Category) {
		return true
	}
	if len(target.view.Items) > 0 && input.At >= target.view.FirstAddedAt+s.cfg.DwellLimitSec {
		return true
	}
	return false
}

func categoriesConflict(target *bag, incoming Category) bool {
	if incoming == CategoryNormal {
		return false
	}
	for _, item := range target.view.Items {
		if item.Category != CategoryNormal && item.Category != incoming {
			return true
		}
	}
	return false
}

func (s *System) openBag(destination string, at int64) int64 {
	id := s.nextBagID
	s.nextBagID++
	s.bags[id] = &bag{view: BagView{
		ID:             id,
		Destination:    destination,
		Status:         BagStatusOpen,
		MaxItems:       s.cfg.MaxItems,
		MaxWeightGrams: s.cfg.MaxWeightGrams,
		DwellLimitSec:  s.cfg.DwellLimitSec,
		FirstAddedAt:   0,
		Items:          make([]Parcel, 0, min(s.cfg.MaxItems, 16)),
	}}
	s.openByStation[destination] = id
	return id
}

func (s *System) sealOpenBag(destination string, at int64) {
	id := s.openByStation[destination]
	target := s.bags[id]
	target.view.Status = BagStatusSealed
	target.view.SealedAt = at
	delete(s.openByStation, destination)
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
