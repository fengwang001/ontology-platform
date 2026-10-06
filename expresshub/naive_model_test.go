package expresshub

import (
	"errors"
	"sort"
)

type naiveParcel struct {
	waybill     string
	destination string
	weight      int64
	category    Category
	bagID       int64
	pending     bool
}

type naiveBag struct {
	id          int64
	destination string
	status      BagStatus
	items       []naiveParcel
	weight      int64
	firstAt     int64
	sealedAt    int64
	vehicle     string
	dispatchAt  int64
	verifiedAt  int64
	missing     []string
	extra       []string
}

type naiveModel struct {
	cfg     Config
	now     int64
	nextID  int64
	bags    []naiveBag
	parcels map[string]naiveParcel
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		nextID:  1,
		parcels: make(map[string]naiveParcel),
	}
}

func codeOf(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func validTestCategory(category Category) bool {
	return category == CategoryNormal || category == CategoryFragile || category == CategoryLiquid
}

func (m *naiveModel) bag(id int64) *naiveBag {
	for i := range m.bags {
		if m.bags[i].id == id {
			return &m.bags[i]
		}
	}
	return nil
}

func (m *naiveModel) openBag(destination string) *naiveBag {
	for i := range m.bags {
		if m.bags[i].destination == destination && m.bags[i].status == BagStatusOpen {
			return &m.bags[i]
		}
	}
	return nil
}

func (m *naiveModel) add(in AddParcelInput) (AddResult, ErrorCode) {
	if in.Waybill == "" || in.Destination == "" || in.WeightGrams < 1 ||
		in.WeightGrams > 10_000_000 || !validTestCategory(in.Category) || in.At < 0 {
		return AddResult{}, InvalidArgument
	}
	if in.At < m.now {
		return AddResult{}, ClockRewound
	}
	if _, exists := m.parcels[in.Waybill]; exists {
		return AddResult{}, BusinessReject
	}
	if in.WeightGrams > m.cfg.MaxWeightGrams {
		return AddResult{}, BusinessReject
	}

	target := m.openBag(in.Destination)
	var sealedID int64
	var openedID int64
	needSeal := false
	if target != nil {
		if len(target.items) >= m.cfg.MaxItems ||
			target.weight+in.WeightGrams > m.cfg.MaxWeightGrams ||
			in.At >= target.firstAt+m.cfg.DwellLimitSec {
			needSeal = true
		}
		if in.Category != CategoryNormal {
			for _, item := range target.items {
				if item.category != CategoryNormal && item.category != in.Category {
					needSeal = true
				}
			}
		}
		if needSeal {
			target.status = BagStatusSealed
			target.sealedAt = in.At
			sealedID = target.id
			target = nil
		}
	}
	if target == nil {
		m.bags = append(m.bags, naiveBag{
			id:          m.nextID,
			destination: in.Destination,
			status:      BagStatusOpen,
			items:       []naiveParcel{},
			firstAt:     in.At,
		})
		target = &m.bags[len(m.bags)-1]
		openedID = target.id
		m.nextID++
	}

	parcel := naiveParcel{
		waybill:     in.Waybill,
		destination: in.Destination,
		weight:      in.WeightGrams,
		category:    in.Category,
		bagID:       target.id,
	}
	target.items = append(target.items, parcel)
	target.weight += in.WeightGrams
	if len(target.items) == 1 {
		target.firstAt = in.At
	}
	m.parcels[in.Waybill] = parcel
	m.now = in.At
	return AddResult{SealedBagID: sealedID, OpenedBagID: openedID, BagID: target.id}, ""
}

func (m *naiveModel) seal(in SealBagInput) (int64, ErrorCode) {
	if in.Destination == "" || in.At < 0 {
		return 0, InvalidArgument
	}
	if in.At < m.now {
		return 0, ClockRewound
	}
	target := m.openBag(in.Destination)
	if target == nil {
		return 0, InvalidState
	}
	target.status = BagStatusSealed
	target.sealedAt = in.At
	m.now = in.At
	return target.id, ""
}

func (m *naiveModel) dispatch(in DispatchBagInput) (int64, ErrorCode) {
	if in.BagID <= 0 || in.VehicleID == "" || in.At < 0 {
		return 0, InvalidArgument
	}
	if in.At < m.now {
		return 0, ClockRewound
	}
	target := m.bag(in.BagID)
	if target == nil {
		return 0, NotFound
	}
	if target.status != BagStatusSealed {
		return 0, InvalidState
	}
	target.status = BagStatusDispatched
	target.vehicle = in.VehicleID
	target.dispatchAt = in.At
	m.now = in.At
	return target.id, ""
}

func (m *naiveModel) verify(in VerifyBagInput) (VerifyResult, ErrorCode) {
	if in.BagID <= 0 || in.Destination == "" || in.At < 0 {
		return VerifyResult{}, InvalidArgument
	}
	seen := make(map[string]bool)
	for _, value := range in.Scanned {
		if value == "" || seen[value] {
			return VerifyResult{}, InvalidArgument
		}
		seen[value] = true
	}
	if in.At < m.now {
		return VerifyResult{}, ClockRewound
	}
	target := m.bag(in.BagID)
	if target == nil {
		return VerifyResult{}, NotFound
	}
	if target.status != BagStatusDispatched {
		return VerifyResult{}, InvalidState
	}
	if target.destination != in.Destination {
		return VerifyResult{}, StationMismatch
	}

	scanned := make(map[string]bool, len(in.Scanned))
	for _, value := range in.Scanned {
		scanned[value] = true
	}
	contained := make(map[string]bool, len(target.items))
	missing := []string{}
	for _, item := range target.items {
		contained[item.waybill] = true
		if !scanned[item.waybill] {
			missing = append(missing, item.waybill)
		}
	}
	extra := []string{}
	for _, value := range in.Scanned {
		if !contained[value] {
			extra = append(extra, value)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	for _, item := range target.items {
		if scanned[item.waybill] {
			delete(m.parcels, item.waybill)
		} else {
			item.pending = true
			m.parcels[item.waybill] = item
		}
	}
	target.status = BagStatusVerified
	target.verifiedAt = in.At
	target.missing = append([]string{}, missing...)
	target.extra = append([]string{}, extra...)
	m.now = in.At
	return VerifyResult{BagID: target.id, Missing: missing, Extra: extra}, ""
}
