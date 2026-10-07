package ncdengine

import (
	"strings"
	"sync"
)

type Engine struct {
	mu       sync.Mutex
	day      int
	accounts map[string]*account
}

func NewEngine(day int) *Engine {
	if day < 0 {
		day = 0
	}
	return &Engine{day: day, accounts: map[string]*account{}}
}

func (e *Engine) Register(input RegisterInput) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validRegister(input) {
		return Result{}, ErrInvalidArgument
	}
	if _, ok := e.accounts[input.CustomerID]; ok {
		return Result{}, ErrInvalidArgument
	}
	if input.StartDay < e.day {
		return Result{}, ErrClockMovedBack
	}
	customer := newAccount(input.Config)
	term := PolicyTerm{VehicleID: input.VehicleID, StartDay: input.StartDay, EndDay: input.StartDay + 365}
	customer.terms = append(customer.terms, term)
	e.accounts[input.CustomerID] = customer
	e.day = input.StartDay
	return resultFromTerm(term), nil
}

func (e *Engine) NewPolicy(input NewPolicyInput) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validIdentity(input.CustomerID, input.VehicleID) || input.Day < 0 {
		return Result{}, ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return Result{}, ErrCustomerNotFound
	}
	if input.Day < e.day {
		return Result{}, ErrClockMovedBack
	}
	if customer.hasActivePolicy(input.Day) {
		return Result{}, ErrActivePolicyExists
	}
	term := PolicyTerm{VehicleID: input.VehicleID, StartDay: input.Day, EndDay: input.Day + 365}
	customer.terms = append(customer.terms, term)
	e.day = input.Day
	return resultFromTerm(term), nil
}

func (e *Engine) Renew(input RenewInput) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validIdentity(input.CustomerID, input.VehicleID) || input.Day < 0 {
		return Result{}, ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return Result{}, ErrCustomerNotFound
	}
	if input.Day < e.day {
		return Result{}, ErrClockMovedBack
	}
	last, exists := customer.latestTerm()
	if !exists || last.VehicleID != input.VehicleID || !customer.isRenewable(last, input.Day) {
		return Result{}, ErrOutsideRenewalWindow
	}
	term := PolicyTerm{
		VehicleID:    input.VehicleID,
		StartDay:     last.EndDay,
		EndDay:       last.EndDay + 365,
		InitialLevel: last.RenewalLevel,
	}
	term.RenewalLevel = nextLevel(term.InitialLevel, customer.termClaims[last.StartDay], last.Protection, customer.config)
	last.Renewed = true
	customer.terms[len(customer.terms)-1] = last
	customer.terms = append(customer.terms, term)
	e.day = input.Day
	return resultFromTerm(term), nil
}

func (e *Engine) BuyProtection(input ProtectInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validIdentity(input.CustomerID, input.VehicleID) || input.Day < 0 {
		return ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return ErrCustomerNotFound
	}
	if input.Day < e.day {
		return ErrClockMovedBack
	}
	index, ok := customer.termAt(input.Day)
	if !ok || customer.terms[index].VehicleID != input.VehicleID {
		return ErrOutsideRenewalWindow
	}
	term := customer.terms[index]
	if term.RenewalLevel < customer.config.ProtectionStart {
		return ErrLevelTooLow
	}
	if term.Protection {
		return ErrProtectionPurchased
	}
	term.Protection = true
	customer.terms[index] = term
	e.day = input.Day
	return nil
}

func (e *Engine) Transfer(input TransferInput) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validIdentity(input.CustomerID, input.ToVehicleID) || !validID(input.FromVehicleID) || input.Day < 0 {
		return Result{}, ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return Result{}, ErrCustomerNotFound
	}
	if input.Day < e.day {
		return Result{}, ErrClockMovedBack
	}
	index, ok := customer.termAt(input.Day)
	if !ok || customer.terms[index].VehicleID != input.FromVehicleID {
		return Result{}, ErrOutsideRenewalWindow
	}
	term := customer.terms[index]
	term.VehicleID = input.ToVehicleID
	customer.terms[index] = term
	e.day = input.Day
	return resultFromTerm(term), nil
}

func (e *Engine) ReportClaim(input ClaimInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validID(input.CustomerID) || !validID(input.VehicleID) || !validID(input.ClaimID) || input.AccidentDay < 0 || input.Liability < 0 || input.Liability > 100 || input.ReportDay < 0 || input.AccidentDay > input.ReportDay {
		return ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return ErrCustomerNotFound
	}
	if input.ReportDay < e.day {
		return ErrClockMovedBack
	}
	if _, exists := customer.claims[input.ClaimID]; exists {
		return ErrClaimExists
	}
	index, ok := customer.termAt(input.AccidentDay)
	if !ok {
		return ErrAccidentUncovered
	}
	if customer.terms[index].VehicleID != input.VehicleID {
		return ErrAccidentUncovered
	}
	claim := Claim{ID: input.ClaimID, AccidentDay: input.AccidentDay, Liability: input.Liability}
	termIndex, _ := customer.addClaim(claim)
	customer.recompute(termIndex, input.CustomerID, claim.ID, false)
	e.day = input.ReportDay
	return nil
}

func (e *Engine) DeleteClaim(input DeleteClaimInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validID(input.CustomerID) || !validID(input.ClaimID) || input.Day < 0 {
		return ErrInvalidArgument
	}
	customer, ok := e.accounts[input.CustomerID]
	if !ok {
		return ErrCustomerNotFound
	}
	if input.Day < e.day {
		return ErrClockMovedBack
	}
	claim, exists := customer.claims[input.ClaimID]
	if !exists {
		return ErrClaimNotFound
	}
	termIndex, _ := customer.removeClaim(claim)
	customer.recompute(termIndex, input.CustomerID, claim.ID, true)
	e.day = input.Day
	return nil
}

func validRegister(input RegisterInput) bool {
	return validIdentity(input.CustomerID, input.VehicleID) && input.StartDay >= 0 && validConfig(input.Config)
}

func validIdentity(customerID string, vehicleID string) bool {
	return validID(customerID) && validID(vehicleID)
}

func validID(value string) bool {
	return strings.TrimSpace(value) != ""
}

func resultFromTerm(term PolicyTerm) Result {
	return Result{Level: term.RenewalLevel, StartDay: term.StartDay, EndDay: term.EndDay}
}
