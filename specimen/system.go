package specimen

import "fmt"

const (
	StatusWaiting    = "待采集"
	StatusCollected  = "已采集待签收"
	StatusAccepted   = "合格"
	StatusTerminated = "终止"
	StatusCanceled   = "已取消"
)

const (
	TransportCold    = "冷藏"
	TransportAmbient = "常温"
)

const (
	ReasonTimeout   = "超时"
	ReasonColdChain = "冷链不符"
	ReasonHemolysis = "溶血超限"
)

const (
	CodeInvalidParameter = "参数非法"
	CodeClockRollback    = "时钟回退"
	CodeNotFound         = "对象不存在"
	CodeDuplicateRequest = "重复申请"
	CodeTubeMismatch     = "管类别不一致"
	CodeStatusMismatch   = "状态不符"
	CodeInvalidTime      = "时间不合理"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type CatalogRequirement struct {
	TubeType           string
	MaxDeliverySeconds int64
	ColdRequired       bool
	HemolysisTolerance int
}

type ApplicationResult struct {
	ApplicationID string
	ItemIDs       []string
}

type SignDecision struct {
	ItemID          string
	Accepted        bool
	RejectionReason string
}

type ItemView struct {
	ID                  string
	ApplicationID       string
	ProjectID           string
	Priority            string
	Status              string
	RejectionCount      int
	LastRejectionReason string
	Collected           bool
	RemainingSeconds    int64
}

type PatientView struct {
	PatientID string
	Items     []ItemView
}

func NewSystem() *System {
	return &System{
		catalog:         make(map[string]catalogItem),
		applications:    make(map[string]*application),
		itemsByID:       make(map[string]*item),
		tubesByID:       make(map[string]*tube),
		activeByPatient: make(map[string]*activeList),
	}
}

func (s *System) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *System) UpsertCatalogItem(now int64, projectID string, requirement CatalogRequirement) error {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(projectID) || !validRequirement(requirement) {
		return errorf(CodeInvalidParameter, "时刻、项目标识或目录要求不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.catalog[projectID] = catalogItem{requirement: requirement}
	s.setClock(now)
	return nil
}

func (s *System) Apply(now int64, patientID string, projectIDs []string, priority string) (ApplicationResult, error) {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(patientID) || !nonEmpty(priority) || !validateProjectIDs(projectIDs) {
		return ApplicationResult{}, errorf(CodeInvalidParameter, "时刻、患者标识、优先级或项目列表不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return ApplicationResult{}, err
	}
	requirements := make([]CatalogRequirement, len(projectIDs))
	for index, projectID := range projectIDs {
		entry, exists := s.catalog[projectID]
		if !exists {
			return ApplicationResult{}, errorf(CodeNotFound, "目录项目 %s 不存在", projectID)
		}
		requirements[index] = entry.requirement
	}
	for _, projectID := range projectIDs {
		if s.hasActiveProjectLocked(patientID, projectID) {
			return ApplicationResult{}, errorf(CodeDuplicateRequest, "患者 %s 已有未终结项目 %s", patientID, projectID)
		}
	}

	s.appSequence++
	applicationID := formatID("A", s.appSequence)
	s.applications[applicationID] = &application{id: applicationID, patientID: patientID}
	result := ApplicationResult{ApplicationID: applicationID, ItemIDs: make([]string, 0, len(projectIDs))}
	for index, projectID := range projectIDs {
		s.itemSequence++
		itemID := formatID("I", s.itemSequence)
		it := &item{
			id:               itemID,
			applicationID:    applicationID,
			patientID:        patientID,
			projectID:        projectID,
			priority:         priority,
			status:           StatusWaiting,
			requirement:      requirements[index],
			enteredWaitingAt: now,
		}
		s.itemsByID[itemID] = it
		result.ItemIDs = append(result.ItemIDs, itemID)
		s.addActiveItemLocked(it)
	}
	s.setClock(now)
	return result, nil
}

func (s *System) Collect(now int64, patientID, tubeType string, itemIDs []string, collectedAt int64) (string, error) {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(patientID) || !nonEmpty(tubeType) || len(itemIDs) == 0 || collectedAt < 0 || collectedAt > 1_000_000_000 {
		return "", errorf(CodeInvalidParameter, "时刻、患者标识、管类别、项目列表或采集时刻不合法")
	}
	seen := make(map[string]struct{}, len(itemIDs))
	for _, itemID := range itemIDs {
		if !nonEmpty(itemID) {
			return "", errorf(CodeInvalidParameter, "项目标识不能为空")
		}
		if _, duplicated := seen[itemID]; duplicated {
			return "", errorf(CodeInvalidParameter, "同一管内项目不能重复")
		}
		seen[itemID] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return "", err
	}

	items := make([]*item, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		it, exists := s.itemsByID[itemID]
		if !exists {
			return "", errorf(CodeNotFound, "项目 %s 不存在", itemID)
		}
		if it.requirement.TubeType != tubeType {
			return "", errorf(CodeTubeMismatch, "项目 %s 需要管类别 %s", itemID, it.requirement.TubeType)
		}
	}
	for _, itemID := range itemIDs {
		it := s.itemsByID[itemID]
		if it.patientID != patientID {
			return "", errorf(CodeStatusMismatch, "项目 %s 不属于患者 %s", itemID, patientID)
		}
		if it.status != StatusWaiting {
			return "", errorf(CodeStatusMismatch, "项目 %s 当前为 %s", itemID, it.status)
		}
		items = append(items, it)
	}
	if collectedAt > now {
		return "", errorf(CodeInvalidTime, "采集时刻 %d 晚于 now %d", collectedAt, now)
	}
	for _, it := range items {
		if collectedAt < it.enteredWaitingAt {
			return "", errorf(CodeInvalidTime, "采集时刻 %d 早于项目 %s 最近待采集时刻 %d", collectedAt, it.id, it.enteredWaitingAt)
		}
	}

	s.tubeSequence++
	tubeID := formatID("T", s.tubeSequence)
	t := &tube{id: tubeID, patientID: patientID, tubeType: tubeType, active: true, itemIDs: append([]string(nil), itemIDs...)}
	s.tubesByID[tubeID] = t
	for _, it := range items {
		it.status = StatusCollected
		it.collectedAt = collectedAt
		it.tubeID = tubeID
	}
	s.setClock(now)
	return tubeID, nil
}

func (s *System) Dispatch(now int64, tubeID, method string) error {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(tubeID) || (method != TransportCold && method != TransportAmbient) {
		return errorf(CodeInvalidParameter, "时刻、管标识或运送方式不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	t, exists := s.tubesByID[tubeID]
	if !exists {
		return errorf(CodeNotFound, "管 %s 不存在", tubeID)
	}
	if !t.active || !s.tubeHasSignableItemLocked(t) {
		return errorf(CodeStatusMismatch, "管 %s 已作废或已签收", tubeID)
	}
	t.method = method
	s.setClock(now)
	return nil
}

func (s *System) Sign(now int64, tubeID string, hemolysis int) ([]SignDecision, error) {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(tubeID) || hemolysis < 0 || hemolysis > 4 {
		return nil, errorf(CodeInvalidParameter, "时刻、管标识或溶血等级不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	t, exists := s.tubesByID[tubeID]
	if !exists {
		return nil, errorf(CodeNotFound, "管 %s 不存在", tubeID)
	}
	if !t.active {
		return nil, errorf(CodeStatusMismatch, "管 %s 已作废或已签收", tubeID)
	}
	signable := s.signableItemsLocked(t)
	if len(signable) == 0 {
		t.active = false
		return nil, errorf(CodeStatusMismatch, "管 %s 已作废或已签收", tubeID)
	}
	if t.method == "" {
		return nil, errorf(CodeStatusMismatch, "管 %s 尚未登记运送方式", tubeID)
	}
	if now < t.collectedBoundLocked(signable) {
		return nil, errorf(CodeInvalidTime, "签收时刻早于管内采集时刻")
	}

	decisions := make([]SignDecision, 0, len(signable))
	for _, it := range signable {
		reason := rejectionReason(now, t.method, hemolysis, it)
		decision := SignDecision{ItemID: it.id, Accepted: reason == "", RejectionReason: reason}
		decisions = append(decisions, decision)
		if reason == "" {
			it.status = StatusAccepted
			it.tubeID = ""
			s.removeActiveItemLocked(it)
			continue
		}
		it.rejectionCount++
		it.lastRejectionReason = reason
		it.tubeID = ""
		if it.rejectionCount == 3 {
			it.status = StatusTerminated
			s.removeActiveItemLocked(it)
		} else {
			it.status = StatusWaiting
			it.enteredWaitingAt = now
		}
		it.collectedAt = 0
	}
	t.active = false
	s.setClock(now)
	return decisions, nil
}

func (s *System) Cancel(now int64, itemID string) error {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(itemID) {
		return errorf(CodeInvalidParameter, "时刻或项目标识不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	it, exists := s.itemsByID[itemID]
	if !exists {
		return errorf(CodeNotFound, "项目 %s 不存在", itemID)
	}
	if it.status != StatusWaiting && it.status != StatusCollected {
		return errorf(CodeStatusMismatch, "项目 %s 当前为终结状态 %s", itemID, it.status)
	}
	tubeID := it.tubeID
	it.status = StatusCanceled
	it.tubeID = ""
	it.collectedAt = 0
	s.removeActiveItemLocked(it)
	if tubeID != "" {
		t := s.tubesByID[tubeID]
		if t != nil && !s.tubeHasSignableItemLocked(t) {
			t.active = false
		}
	}
	s.setClock(now)
	return nil
}

func (s *System) Query(now int64, patientID string) (*PatientView, error) {
	if now < 0 || now > 1_000_000_000 || !nonEmpty(patientID) {
		return nil, errorf(CodeInvalidParameter, "时刻或患者标识不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	view := &PatientView{PatientID: patientID, Items: make([]ItemView, 0)}
	list := s.activeByPatient[patientID]
	if list != nil {
		for node := list.first; node != nil; node = node.next {
			it := node.item
			itemView := ItemView{
				ID:                  it.id,
				ApplicationID:       it.applicationID,
				ProjectID:           it.projectID,
				Priority:            it.priority,
				Status:              it.status,
				RejectionCount:      it.rejectionCount,
				LastRejectionReason: it.lastRejectionReason,
				Collected:           it.status == StatusCollected,
			}
			if it.status == StatusCollected {
				itemView.RemainingSeconds = it.requirement.MaxDeliverySeconds - (now - it.collectedAt)
			}
			view.Items = append(view.Items, itemView)
		}
	}
	s.setClock(now)
	return view, nil
}
