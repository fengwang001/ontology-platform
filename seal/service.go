package seal

import (
	"container/heap"
	"sync"
)

type receiptDeadline struct {
	at          int64
	application string
	employee    string
}

type receiptQueue []receiptDeadline

type employeeReceiptItem struct {
	receiptDeadline
	index int
}

type employeeReceiptQueue []*employeeReceiptItem

func (q receiptQueue) Len() int { return len(q) }

func (q receiptQueue) Less(i int, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	if q[i].application != q[j].application {
		return q[i].application < q[j].application
	}
	return q[i].employee < q[j].employee
}

func (q receiptQueue) Swap(i int, j int) { q[i], q[j] = q[j], q[i] }

func (q *receiptQueue) Push(value any) { *q = append(*q, value.(receiptDeadline)) }

func (q *receiptQueue) Pop() any {
	old := *q
	last := old[len(old)-1]
	*q = old[:len(old)-1]
	return last
}

func (q employeeReceiptQueue) Len() int { return len(q) }

func (q employeeReceiptQueue) Less(i int, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	if q[i].application != q[j].application {
		return q[i].application < q[j].application
	}
	return q[i].employee < q[j].employee
}

func (q employeeReceiptQueue) Swap(i int, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *employeeReceiptQueue) Push(value any) {
	item := value.(*employeeReceiptItem)
	item.index = len(*q)
	*q = append(*q, item)
}

func (q *employeeReceiptQueue) Pop() any {
	old := *q
	last := old[len(old)-1]
	*q = old[:len(old)-1]
	return last
}

type Service struct {
	mu                   sync.Mutex
	config               Config
	lastAcceptedAt       int64
	stamps               map[string]*Stamp
	authorizations       map[string]*Authorization
	authOrder            []string
	applications         map[string]*Application
	dailyDay             map[string]int64
	dailyCount           map[string]int64
	receipts             receiptQueue
	employeeReceipts     map[string]employeeReceiptQueue
	employeeReceiptItems map[string]*employeeReceiptItem
	pendingReceipts      map[string]int
	overdueReceipts      map[string]int
	frozen               map[string]bool
}

func NewService(config Config) *Service {
	if config.ApprovalValiditySeconds <= 0 || config.ReceiptDeadlineSeconds <= 0 {
		panic("seal: positive validity and receipt deadlines are required")
	}
	if config.FirstAmountThreshold < 0 || config.SecondAmountThreshold < config.FirstAmountThreshold {
		panic("seal: invalid approval amount thresholds")
	}
	if config.DualPresenceThreshold == 0 {
		config.DualPresenceThreshold = config.SecondAmountThreshold
	}
	if config.DualPresenceThreshold < 0 {
		panic("seal: invalid dual-presence threshold")
	}
	return &Service{
		config:               config,
		lastAcceptedAt:       -1,
		stamps:               map[string]*Stamp{},
		authorizations:       map[string]*Authorization{},
		authOrder:            nil,
		applications:         map[string]*Application{},
		dailyDay:             map[string]int64{},
		dailyCount:           map[string]int64{},
		employeeReceipts:     map[string]employeeReceiptQueue{},
		employeeReceiptItems: map[string]*employeeReceiptItem{},
		pendingReceipts:      map[string]int{},
		overdueReceipts:      map[string]int{},
		frozen:               map[string]bool{},
	}
}

func (s *Service) CreateStamp(input CreateStampInput) error {
	if input.Now < 0 || input.StampID == "" || input.Category == "" ||
		input.CustodianA == "" || input.CustodianB == "" || input.CustodianA == input.CustodianB {
		return newError(KindInvalidParameter, "invalid stamp input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	if _, exists := s.stamps[input.StampID]; exists {
		return newError(KindInvalidState, "stamp already exists")
	}
	s.advanceClock(input.Now)
	s.stamps[input.StampID] = &Stamp{
		ID:         input.StampID,
		Category:   input.Category,
		Status:     StampNormal,
		CustodianA: input.CustodianA,
		CustodianB: input.CustodianB,
	}
	return nil
}

func (s *Service) SetStampStatus(input SetStampStatusInput) error {
	if input.Now < 0 || input.StampID == "" {
		return newError(KindInvalidParameter, "invalid stamp status input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	stamp, exists := s.stamps[input.StampID]
	if !exists {
		return newError(KindNotFound, "stamp not found")
	}
	nextStatus := StampNormal
	if input.Disabled {
		nextStatus = StampDisabled
	}
	if stamp.Status == nextStatus {
		s.advanceClock(input.Now)
		return nil
	}
	s.advanceClock(input.Now)
	stamp.Status = nextStatus
	if input.Disabled {
		for _, application := range s.applications {
			if application.StampID == input.StampID && application.State != StateExecuted &&
				application.State != StateRejected && application.State != StateInvalidated {
				application.State = StateInvalidated
			}
		}
	}
	return nil
}

func (s *Service) GrantAuthorization(input GrantAuthorizationInput) error {
	if input.Now < 0 || input.AuthorizationID == "" || input.EmployeeID == "" ||
		input.StampID == "" || input.MaxAmount < 0 || input.DailyLimit <= 0 ||
		input.StartsAt < 0 || input.EndsAt <= input.StartsAt || len(input.MaterialKinds) == 0 {
		return newError(KindInvalidParameter, "invalid authorization input")
	}
	kinds := make(map[string]struct{}, len(input.MaterialKinds))
	for _, kind := range input.MaterialKinds {
		if kind == "" {
			return newError(KindInvalidParameter, "material kind must not be empty")
		}
		kinds[kind] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	if _, exists := s.authorizations[input.AuthorizationID]; exists {
		return newError(KindInvalidState, "authorization already exists")
	}
	if _, exists := s.stamps[input.StampID]; !exists {
		return newError(KindNotFound, "stamp not found")
	}
	s.advanceClock(input.Now)
	s.authorizations[input.AuthorizationID] = &Authorization{
		ID:            input.AuthorizationID,
		EmployeeID:    input.EmployeeID,
		StampID:       input.StampID,
		MaterialKinds: kinds,
		MaxAmount:     input.MaxAmount,
		DailyLimit:    input.DailyLimit,
		StartsAt:      input.StartsAt,
		EndsAt:        input.EndsAt,
	}
	s.authOrder = append(s.authOrder, input.AuthorizationID)
	return nil
}

func (s *Service) RevokeAuthorization(input RevokeAuthorizationInput) error {
	if input.Now < 0 || input.AuthorizationID == "" {
		return newError(KindInvalidParameter, "invalid revocation input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	authorization, exists := s.authorizations[input.AuthorizationID]
	if !exists {
		return newError(KindNotFound, "authorization not found")
	}
	if authorization.Revoked {
		return newError(KindInvalidState, "authorization already revoked")
	}
	s.advanceClock(input.Now)
	authorization.Revoked = true
	return nil
}

func (s *Service) SubmitApplication(input SubmitApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.ApplicantID == "" ||
		input.StampID == "" || input.MaterialKind == "" || input.Amount < 0 {
		return newError(KindInvalidParameter, "invalid application input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	if _, exists := s.stamps[input.StampID]; !exists {
		return newError(KindNotFound, "stamp not found")
	}
	if _, exists := s.applications[input.ApplicationID]; exists {
		return newError(KindInvalidState, "application already exists")
	}
	if s.isFrozenAt(input.ApplicantID, input.Now) {
		return newError(KindFrozen, "applicant is frozen")
	}
	authorization, err := s.findValidAuthorization(input.ApplicantID, input.StampID, input.Now)
	if err != nil {
		return err
	}
	s.advanceClock(input.Now)
	s.applications[input.ApplicationID] = &Application{
		ID:           input.ApplicationID,
		ApplicantID:  input.ApplicantID,
		StampID:      input.StampID,
		AuthID:       authorization.ID,
		MaterialKind: input.MaterialKind,
		Amount:       input.Amount,
		State:        StatePending,
		Approvals:    map[string]bool{},
		Confirmers:   map[string]struct{}{},
	}
	return nil
}

func (s *Service) DecideApplication(input DecisionInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.ApproverID == "" {
		return newError(KindInvalidParameter, "invalid decision input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	application, exists := s.applications[input.ApplicationID]
	if !exists {
		return newError(KindNotFound, "application not found")
	}
	stamp := s.stamps[application.StampID]
	if application.State != StatePending {
		return newError(KindInvalidState, "application is not pending")
	}
	if input.ApproverID == application.ApplicantID {
		return newError(KindInvalidState, "applicant cannot approve")
	}
	if _, decided := application.Approvals[input.ApproverID]; decided {
		return newError(KindInvalidState, "approver already decided")
	}
	required := s.requiredApprovers(application.Amount)
	if !input.Approve {
		s.advanceClock(input.Now)
		application.Approvals[input.ApproverID] = false
		application.State = StateRejected
		return nil
	}
	if len(application.Approvals)+1 == required && required == 3 &&
		!s.hasCustodianApproval(application, stamp, input.ApproverID) {
		return newError(KindInvalidState, "highest tier requires a custodian approver")
	}
	s.advanceClock(input.Now)
	application.Approvals[input.ApproverID] = true
	if len(application.Approvals) == required {
		application.State = StateApproved
		application.ExpiresAt = input.Now + s.config.ApprovalValiditySeconds
	}
	return nil
}

func (s *Service) ConfirmPresence(input PresenceInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.CustodianID == "" {
		return newError(KindInvalidParameter, "invalid presence input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	application, exists := s.applications[input.ApplicationID]
	if !exists {
		return newError(KindNotFound, "application not found")
	}
	stamp := s.stamps[application.StampID]
	if application.State != StateApproved || input.Now > application.ExpiresAt {
		return newError(KindInvalidState, "application is not executable")
	}
	if input.CustodianID != stamp.CustodianA && input.CustodianID != stamp.CustodianB {
		return newError(KindInvalidState, "custodian must confirm presence")
	}
	if _, alreadyConfirmed := application.Confirmers[input.CustodianID]; alreadyConfirmed {
		return newError(KindInvalidState, "custodian already confirmed")
	}
	s.advanceClock(input.Now)
	application.Confirmers[input.CustodianID] = struct{}{}
	return nil
}

func (s *Service) ExecuteApplication(input ApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" {
		return newError(KindInvalidParameter, "invalid execution input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	application, exists := s.applications[input.ApplicationID]
	if !exists {
		return newError(KindNotFound, "application not found")
	}
	stamp := s.stamps[application.StampID]
	if application.State != StateApproved {
		return newError(KindInvalidState, "application is not approved")
	}
	if input.Now > application.ExpiresAt {
		return newError(KindInvalidState, "application has expired")
	}
	if stamp.Status != StampNormal {
		return newError(KindInvalidState, "stamp is disabled")
	}
	if s.isFrozenAt(application.ApplicantID, input.Now) {
		return newError(KindFrozen, "applicant is frozen")
	}
	authorization := s.authorizations[application.AuthID]
	if authorization == nil || authorization.Revoked ||
		input.Now < authorization.StartsAt || input.Now >= authorization.EndsAt {
		return newError(KindAuthorization, "authorization is not effective")
	}
	if _, allowed := authorization.MaterialKinds[application.MaterialKind]; !allowed {
		return newError(KindLimitExceeded, "material kind is not authorized")
	}
	if application.Amount > authorization.MaxAmount {
		return newError(KindLimitExceeded, "amount exceeds single-use limit")
	}
	day := input.Now / 86400
	currentCount := s.dailyCount[authorization.ID]
	if s.dailyDay[authorization.ID] != day {
		currentCount = 0
	}
	if currentCount >= authorization.DailyLimit {
		return newError(KindDailyLimitExceeded, "daily use limit reached")
	}
	if application.Amount >= s.config.DualPresenceThreshold &&
		!s.hasBothCustodians(application, stamp) {
		return newError(KindPresenceRequired, "two distinct custodians must confirm presence")
	}
	s.advanceClock(input.Now)
	application.State = StateExecuted
	application.ExecutedAt = input.Now
	application.ReceiptDueAt = input.Now + s.config.ReceiptDeadlineSeconds
	if s.dailyDay[authorization.ID] != day {
		s.dailyDay[authorization.ID] = day
		currentCount = 0
	}
	s.dailyCount[authorization.ID] = currentCount + 1
	s.pushReceiptDeadline(receiptDeadline{
		at:          application.ReceiptDueAt,
		application: application.ID,
		employee:    application.ApplicantID,
	})
	s.pendingReceipts[application.ApplicantID]++
	return nil
}

func (s *Service) RegisterReceipt(input ApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" {
		return newError(KindInvalidParameter, "invalid receipt input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	application, exists := s.applications[input.ApplicationID]
	if !exists {
		return newError(KindNotFound, "application not found")
	}
	if application.State != StateExecuted {
		return newError(KindInvalidState, "application is not executed")
	}
	if application.ReceiptDone {
		return newError(KindInvalidState, "receipt already registered")
	}
	s.advanceClock(input.Now)
	application.ReceiptDone = true
	if application.Overdue {
		s.overdueReceipts[application.ApplicantID]--
		if s.overdueReceipts[application.ApplicantID] == 0 {
			delete(s.overdueReceipts, application.ApplicantID)
			delete(s.frozen, application.ApplicantID)
		}
	} else {
		s.pendingReceipts[application.ApplicantID]--
		if s.pendingReceipts[application.ApplicantID] == 0 {
			delete(s.pendingReceipts, application.ApplicantID)
		}
		s.removeEmployeeReceipt(application.ApplicantID, application.ID)
	}
	return nil
}

func (s *Service) MarkVoid(input MarkVoidInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.CustodianID == "" {
		return newError(KindInvalidParameter, "invalid void input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	application, exists := s.applications[input.ApplicationID]
	if !exists {
		return newError(KindNotFound, "application not found")
	}
	stamp := s.stamps[application.StampID]
	if application.State != StateExecuted {
		return newError(KindInvalidState, "only executed applications can be voided")
	}
	if input.CustodianID != stamp.CustodianA && input.CustodianID != stamp.CustodianB {
		return newError(KindInvalidState, "only a custodian can void an application")
	}
	if application.Voided {
		return newError(KindInvalidState, "application already voided")
	}
	s.advanceClock(input.Now)
	application.Voided = true
	return nil
}

func (s *Service) findValidAuthorization(employeeID string, stampID string, now int64) (*Authorization, error) {
	for _, authorizationID := range s.authOrder {
		authorization := s.authorizations[authorizationID]
		if authorization.EmployeeID == employeeID && authorization.StampID == stampID &&
			!authorization.Revoked && now >= authorization.StartsAt && now < authorization.EndsAt {
			return authorization, nil
		}
	}
	return nil, newError(KindAuthorization, "no effective authorization")
}

func (s *Service) requiredApprovers(amount int64) int {
	if amount < s.config.FirstAmountThreshold {
		return 1
	}
	if amount < s.config.SecondAmountThreshold {
		return 2
	}
	return 3
}

func (s *Service) hasCustodianApproval(application *Application, stamp *Stamp, currentApprover string) bool {
	if currentApprover == stamp.CustodianA || currentApprover == stamp.CustodianB {
		return true
	}
	for approverID, approved := range application.Approvals {
		if approved && (approverID == stamp.CustodianA || approverID == stamp.CustodianB) {
			return true
		}
	}
	return false
}

func (s *Service) advanceClock(now int64) error {
	if now < s.lastAcceptedAt {
		return newError(KindClockRollback, "now is before last accepted operation")
	}
	s.lastAcceptedAt = now
	for s.receipts.Len() > 0 && s.receipts[0].at < now {
		item := heap.Pop(&s.receipts).(receiptDeadline)
		employeeItem := s.employeeReceiptItems[item.application]
		if employeeItem != nil {
			employeeQueue := s.employeeReceipts[item.employee]
			heap.Remove(&employeeQueue, employeeItem.index)
			delete(s.employeeReceiptItems, item.application)
			if employeeQueue.Len() == 0 {
				delete(s.employeeReceipts, item.employee)
			} else {
				s.employeeReceipts[item.employee] = employeeQueue
			}
		}
		application := s.applications[item.application]
		if application == nil || application.ReceiptDone {
			continue
		}
		application.Overdue = true
		s.pendingReceipts[item.employee]--
		if s.pendingReceipts[item.employee] == 0 {
			delete(s.pendingReceipts, item.employee)
		}
		s.overdueReceipts[item.employee]++
		s.frozen[item.employee] = true
	}
	return nil
}

func (s *Service) hasBothCustodians(application *Application, stamp *Stamp) bool {
	_, hasA := application.Confirmers[stamp.CustodianA]
	_, hasB := application.Confirmers[stamp.CustodianB]
	return hasA && hasB
}

func (s *Service) pushReceiptDeadline(item receiptDeadline) {
	heap.Push(&s.receipts, item)
	employeeQueue := s.employeeReceipts[item.employee]
	employeeItem := &employeeReceiptItem{receiptDeadline: item}
	heap.Push(&employeeQueue, employeeItem)
	s.employeeReceipts[item.employee] = employeeQueue
	s.employeeReceiptItems[item.application] = employeeItem
}

func (s *Service) removeEmployeeReceipt(employeeID string, applicationID string) {
	employeeItem := s.employeeReceiptItems[applicationID]
	if employeeItem == nil {
		return
	}
	employeeQueue := s.employeeReceipts[employeeID]
	heap.Remove(&employeeQueue, employeeItem.index)
	delete(s.employeeReceiptItems, applicationID)
	if employeeQueue.Len() == 0 {
		delete(s.employeeReceipts, employeeID)
	} else {
		s.employeeReceipts[employeeID] = employeeQueue
	}
}

func (s *Service) checkClock(now int64) error {
	if now < s.lastAcceptedAt {
		return newError(KindClockRollback, "now is before last accepted operation")
	}
	return nil
}

func (s *Service) isFrozenAt(employeeID string, now int64) bool {
	if s.frozen[employeeID] {
		return true
	}
	employeeQueue := s.employeeReceipts[employeeID]
	if employeeQueue.Len() == 0 {
		return false
	}
	head := employeeQueue[0]
	return head.at < now && !s.applications[head.application].ReceiptDone
}
