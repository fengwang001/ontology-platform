package ontology

import "fmt"

func effectiveStatus(auth *authRecord, now int64) string {
	if (auth.status == StatusPending || auth.status == StatusValid) && now >= auth.expires {
		return StatusExpired
	}
	return auth.status
}

func (m *StateMachine) deriveOrderStatus(order *orderRecord, now int64) string {
	if order.status == StatusValid {
		return StatusValid
	}
	if now >= order.expires {
		return StatusInvalid
	}

	allValid := true
	for _, authID := range order.authIDs {
		auth := m.auths[authID-1]
		switch effectiveStatus(auth, now) {
		case StatusValid:
		case StatusPending:
			allValid = false
		default:
			return StatusInvalid
		}
	}
	if allValid {
		return StatusReady
	}
	return StatusPending
}

func (m *StateMachine) copyAuthorization(auth *authRecord) *Authorization {
	return &Authorization{
		ID:         fmt.Sprintf("z%d", auth.id),
		Account:    []byte(auth.account),
		Identifier: auth.identifier,
		Status:     auth.status,
		Expires:    auth.expires,
	}
}

func (m *StateMachine) copyOrder(order *orderRecord) *Order {
	result := &Order{
		ID:               fmt.Sprintf("o%d", order.id),
		Account:          []byte(order.account),
		Identifiers:      cloneStrings(order.identifiers),
		AuthorizationIDs: make([]string, len(order.authIDs)),
		Expires:          order.expires,
		Status:           order.status,
		CertSerial:       order.certSerial,
	}
	for i, authID := range order.authIDs {
		result.AuthorizationIDs[i] = fmt.Sprintf("z%d", authID)
	}
	return result
}

func cloneStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}
