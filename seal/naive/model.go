package naive

import "ontology/seal"

type Execution struct {
	ApplicationID   string
	AuthorizationID string
	At              int64
	Day             int64
}

type Model struct {
	Config     seal.Config
	LastNow    int64
	Stamps     map[string]seal.Stamp
	Auths      map[string]seal.Authorization
	AuthOrder  []string
	Apps       map[string]seal.Application
	Executions []Execution
}

func New(config seal.Config) *Model {
	return &Model{Config: config, LastNow: -1, Stamps: map[string]seal.Stamp{}, Auths: map[string]seal.Authorization{}, Apps: map[string]seal.Application{}}
}

func fail(kindValue seal.ErrorKind, message string) error {
	return seal.Error{Kind: kindValue, Msg: message}
}

func (m *Model) CreateStamp(input seal.CreateStampInput) error {
	if input.Now < 0 || input.StampID == "" || input.Category == "" ||
		input.CustodianA == "" || input.CustodianB == "" || input.CustodianA == input.CustodianB {
		return fail(seal.KindInvalidParameter, "invalid stamp")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	if _, exists := m.Stamps[input.StampID]; exists {
		return fail(seal.KindInvalidState, "exists")
	}
	m.LastNow = input.Now
	m.refreshOverdue(input.Now)
	m.Stamps[input.StampID] = seal.Stamp{
		ID: input.StampID, Category: input.Category, Status: seal.StampNormal,
		CustodianA: input.CustodianA, CustodianB: input.CustodianB,
	}
	return nil
}
func (m *Model) SetStampStatus(input seal.SetStampStatusInput) error {
	if input.Now < 0 || input.StampID == "" {
		return fail(seal.KindInvalidParameter, "invalid status")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	stamp, exists := m.Stamps[input.StampID]
	if !exists {
		return fail(seal.KindNotFound, "stamp")
	}
	m.LastNow = input.Now
	m.refreshOverdue(input.Now)
	next := seal.StampNormal
	if input.Disabled {
		next = seal.StampDisabled
	}
	if stamp.Status == next {
		return nil
	}
	stamp.Status = next
	m.Stamps[input.StampID] = stamp
	if input.Disabled {
		for id, application := range m.Apps {
			if application.StampID == input.StampID && application.State != seal.StateExecuted &&
				application.State != seal.StateRejected && application.State != seal.StateInvalidated {
				application.State = seal.StateInvalidated
				m.Apps[id] = application
			}
		}
	}
	return nil
}
func (m *Model) Grant(input seal.GrantAuthorizationInput) error {
	if input.Now < 0 || input.AuthorizationID == "" || input.EmployeeID == "" ||
		input.StampID == "" || input.MaxAmount < 0 || input.DailyLimit <= 0 ||
		input.StartsAt < 0 || input.EndsAt <= input.StartsAt || len(input.MaterialKinds) == 0 {
		return fail(seal.KindInvalidParameter, "invalid grant")
	}
	kinds := map[string]struct{}{}
	for _, kindName := range input.MaterialKinds {
		if kindName == "" {
			return fail(seal.KindInvalidParameter, "empty kind")
		}
		kinds[kindName] = struct{}{}
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	if _, exists := m.Auths[input.AuthorizationID]; exists {
		return fail(seal.KindInvalidState, "exists")
	}
	if _, exists := m.Stamps[input.StampID]; !exists {
		return fail(seal.KindNotFound, "stamp")
	}
	m.LastNow = input.Now
	m.refreshOverdue(input.Now)
	m.Auths[input.AuthorizationID] = seal.Authorization{
		ID: input.AuthorizationID, EmployeeID: input.EmployeeID, StampID: input.StampID,
		MaterialKinds: kinds, MaxAmount: input.MaxAmount, DailyLimit: input.DailyLimit,
		StartsAt: input.StartsAt, EndsAt: input.EndsAt,
	}
	m.AuthOrder = append(m.AuthOrder, input.AuthorizationID)
	return nil
}
func (m *Model) Revoke(input seal.RevokeAuthorizationInput) error {
	if input.Now < 0 || input.AuthorizationID == "" {
		return fail(seal.KindInvalidParameter, "invalid revoke")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	authorization, exists := m.Auths[input.AuthorizationID]
	if !exists {
		return fail(seal.KindNotFound, "auth")
	}
	if authorization.Revoked {
		return fail(seal.KindInvalidState, "revoked")
	}
	m.LastNow = input.Now
	m.refreshOverdue(input.Now)
	authorization.Revoked = true
	m.Auths[input.AuthorizationID] = authorization
	return nil
}
func (m *Model) Submit(input seal.SubmitApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.ApplicantID == "" ||
		input.StampID == "" || input.MaterialKind == "" || input.Amount < 0 {
		return fail(seal.KindInvalidParameter, "invalid submit")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	if _, exists := m.Stamps[input.StampID]; !exists {
		return fail(seal.KindNotFound, "stamp")
	}
	if _, exists := m.Apps[input.ApplicationID]; exists {
		return fail(seal.KindInvalidState, "exists")
	}
	if m.Frozen(input.ApplicantID, input.Now) {
		return fail(seal.KindFrozen, "frozen")
	}
	authorization, err := m.auth(input.ApplicantID, input.StampID, input.Now)
	if err != nil {
		return err
	}
	m.LastNow = input.Now
	m.refreshOverdue(input.Now)
	m.Apps[input.ApplicationID] = seal.Application{
		ID: input.ApplicationID, ApplicantID: input.ApplicantID, StampID: input.StampID,
		AuthID: authorization.ID, MaterialKind: input.MaterialKind, Amount: input.Amount,
		State: seal.StatePending, Approvals: map[string]bool{}, Confirmers: map[string]struct{}{},
	}
	return nil
}
func (m *Model) Decide(input seal.DecisionInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.ApproverID == "" {
		return fail(seal.KindInvalidParameter, "invalid decide")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	application, exists := m.Apps[input.ApplicationID]
	if !exists {
		return fail(seal.KindNotFound, "app")
	}
	stamp := m.Stamps[application.StampID]
	if application.State != seal.StatePending || input.ApproverID == application.ApplicantID {
		return fail(seal.KindInvalidState, "cannot decide")
	}
	if _, exists := application.Approvals[input.ApproverID]; exists {
		return fail(seal.KindInvalidState, "repeat decision")
	}
	required := m.required(application.Amount)
	if !input.Approve {
		m.refreshOverdue(input.Now)
		m.LastNow = input.Now
		application = m.Apps[input.ApplicationID]
		application.Approvals[input.ApproverID] = false
		application.State = seal.StateRejected
		m.Apps[input.ApplicationID] = application
		return nil
	}
	if len(application.Approvals)+1 == required && required == 3 &&
		!m.custodianApproval(application, stamp, input.ApproverID) {
		return fail(seal.KindInvalidState, "custodian required")
	}
	m.refreshOverdue(input.Now)
	m.LastNow = input.Now
	application = m.Apps[input.ApplicationID]
	application.Approvals[input.ApproverID] = true
	if len(application.Approvals) == required {
		application.State = seal.StateApproved
		application.ExpiresAt = input.Now + m.Config.ApprovalValiditySeconds
	}
	m.Apps[input.ApplicationID] = application
	return nil
}
func (m *Model) Confirm(input seal.PresenceInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.CustodianID == "" {
		return fail(seal.KindInvalidParameter, "invalid presence")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	application, exists := m.Apps[input.ApplicationID]
	if !exists {
		return fail(seal.KindNotFound, "app")
	}
	stamp := m.Stamps[application.StampID]
	if application.State != seal.StateApproved || input.Now > application.ExpiresAt {
		return fail(seal.KindInvalidState, "not executable")
	}
	if input.CustodianID != stamp.CustodianA && input.CustodianID != stamp.CustodianB {
		return fail(seal.KindInvalidState, "not custodian")
	}
	if _, exists := application.Confirmers[input.CustodianID]; exists {
		return fail(seal.KindInvalidState, "repeat custodian")
	}
	m.refreshOverdue(input.Now)
	m.LastNow = input.Now
	application = m.Apps[input.ApplicationID]
	application.Confirmers[input.CustodianID] = struct{}{}
	m.Apps[input.ApplicationID] = application
	return nil
}
func (m *Model) Execute(input seal.ApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" {
		return fail(seal.KindInvalidParameter, "invalid execute")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	application, exists := m.Apps[input.ApplicationID]
	if !exists {
		return fail(seal.KindNotFound, "app")
	}
	stamp := m.Stamps[application.StampID]
	if application.State != seal.StateApproved {
		return fail(seal.KindInvalidState, "not approved")
	}
	if input.Now > application.ExpiresAt {
		return fail(seal.KindInvalidState, "expired")
	}
	if stamp.Status != seal.StampNormal {
		return fail(seal.KindInvalidState, "disabled")
	}
	if m.Frozen(application.ApplicantID, input.Now) {
		return fail(seal.KindFrozen, "frozen")
	}
	authorization := m.Auths[application.AuthID]
	if authorization.Revoked || input.Now < authorization.StartsAt || input.Now >= authorization.EndsAt {
		return fail(seal.KindAuthorization, "authorization")
	}
	if _, allowed := authorization.MaterialKinds[application.MaterialKind]; !allowed ||
		application.Amount > authorization.MaxAmount {
		return fail(seal.KindLimitExceeded, "limit")
	}
	if m.Daily(authorization.ID, input.Now) >= authorization.DailyLimit {
		return fail(seal.KindDailyLimitExceeded, "daily")
	}
	if application.Amount >= m.Config.DualPresenceThreshold &&
		(!m.confirmed(application, stamp.CustodianA) || !m.confirmed(application, stamp.CustodianB)) {
		return fail(seal.KindPresenceRequired, "presence")
	}
	m.refreshOverdue(input.Now)
	m.LastNow = input.Now
	application = m.Apps[input.ApplicationID]
	application.State = seal.StateExecuted
	application.ExecutedAt = input.Now
	application.ReceiptDueAt = input.Now + m.Config.ReceiptDeadlineSeconds
	m.Apps[input.ApplicationID] = application
	m.Executions = append(m.Executions, Execution{
		ApplicationID: application.ID, AuthorizationID: authorization.ID,
		At: input.Now, Day: input.Now / 86400,
	})
	return nil
}
func (m *Model) Receipt(input seal.ApplicationInput) error {
	if input.Now < 0 || input.ApplicationID == "" {
		return fail(seal.KindInvalidParameter, "invalid receipt")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	application, exists := m.Apps[input.ApplicationID]
	if !exists {
		return fail(seal.KindNotFound, "app")
	}
	if application.State != seal.StateExecuted || application.ReceiptDone {
		return fail(seal.KindInvalidState, "cannot receipt")
	}
	m.refreshOverdue(input.Now)
	m.LastNow = input.Now
	application = m.Apps[input.ApplicationID]
	application.ReceiptDone = true
	m.Apps[input.ApplicationID] = application
	return nil
}
func (m *Model) Void(input seal.MarkVoidInput) error {
	if input.Now < 0 || input.ApplicationID == "" || input.CustodianID == "" {
		return fail(seal.KindInvalidParameter, "invalid void")
	}
	if input.Now < m.LastNow {
		return fail(seal.KindClockRollback, "rollback")
	}
	application, exists := m.Apps[input.ApplicationID]
	if !exists {
		return fail(seal.KindNotFound, "app")
	}
	stamp := m.Stamps[application.StampID]
	if application.State != seal.StateExecuted {
		return fail(seal.KindInvalidState, "not executed")
	}
	if input.CustodianID != stamp.CustodianA && input.CustodianID != stamp.CustodianB {
		return fail(seal.KindInvalidState, "not custodian")
	}
	if application.Voided {
		return fail(seal.KindInvalidState, "voided")
	}
	m.refreshOverdue(input.Now)
	m.LastNow = input.Now
	application = m.Apps[input.ApplicationID]
	application.Voided = true
	m.Apps[input.ApplicationID] = application
	return nil
}
func (m *Model) Frozen(employeeID string, now int64) bool {
	for _, application := range m.Apps {
		if application.ApplicantID == employeeID && application.State == seal.StateExecuted &&
			!application.ReceiptDone && now > application.ReceiptDueAt {
			return true
		}
		if application.ApplicantID == employeeID && application.Overdue {
			for _, other := range m.Apps {
				if other.ApplicantID == employeeID && other.Overdue && !other.ReceiptDone {
					return true
				}
			}
		}
	}
	return false
}

func (m *Model) Daily(authorizationID string, now int64) int64 {
	var count int64
	for _, execution := range m.Executions {
		if execution.AuthorizationID == authorizationID && execution.Day == now/86400 {
			count++
		}
	}
	return count
}

func (m *Model) auth(employeeID string, stampID string, now int64) (seal.Authorization, error) {
	for _, id := range m.AuthOrder {
		authorization := m.Auths[id]
		if authorization.EmployeeID == employeeID && authorization.StampID == stampID &&
			!authorization.Revoked && now >= authorization.StartsAt && now < authorization.EndsAt {
			return authorization, nil
		}
	}
	return seal.Authorization{}, fail(seal.KindAuthorization, "authorization")
}

func (m *Model) required(amount int64) int {
	if amount < m.Config.FirstAmountThreshold {
		return 1
	}
	if amount < m.Config.SecondAmountThreshold {
		return 2
	}
	return 3
}

func (m *Model) custodianApproval(application seal.Application, stamp seal.Stamp, approver string) bool {
	if approver == stamp.CustodianA || approver == stamp.CustodianB {
		return true
	}
	for approverID, approved := range application.Approvals {
		if approved && (approverID == stamp.CustodianA || approverID == stamp.CustodianB) {
			return true
		}
	}
	return false
}

func (m *Model) confirmed(application seal.Application, custodian string) bool {
	_, exists := application.Confirmers[custodian]
	return exists
}

func (m *Model) refreshOverdue(now int64) {
	for id, application := range m.Apps {
		if application.State == seal.StateExecuted && !application.ReceiptDone &&
			now > application.ReceiptDueAt && !application.Overdue {
			application.Overdue = true
			m.Apps[id] = application
		}
	}
}
