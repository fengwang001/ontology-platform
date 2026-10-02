package ontology

import (
	"bytes"
	"sync"
	"sync/atomic"
)

type MACFunc func(key, data []byte) []byte

type Token struct {
	ID      []byte
	Caveats [][]byte
	Sig     []byte
}

type Request struct {
	Op     string
	Res    string
	Amount uint64
}

type ErrorCode string

const (
	ErrInvalid      ErrorCode = "invalid"
	ErrClockRewound ErrorCode = "clock_rewound"
	ErrBadSignature ErrorCode = "bad_signature"
	ErrRevoked      ErrorCode = "revoked"
	ErrCaveat       ErrorCode = "caveat_unsatisfied"
	ErrLimit        ErrorCode = "limit_exceeded"
)

type CaveatKind string

const (
	CaveatUnknown     CaveatKind = "unknown"
	CaveatMalformed   CaveatKind = "malformed"
	CaveatUnsatisfied CaveatKind = "unsatisfied"
)

type Error struct {
	Code       ErrorCode
	Index      int
	Prefix     string
	Kind       CaveatKind
	RevokedAt  int
	MaxCaveats int
	MaxRecords int
}

func (e *Error) Error() string { return string(e.Code) }

type RevokeResult struct {
	Expired bool
}

type Service struct {
	rootKey        []byte
	mac            MACFunc
	maxCaveats     int
	maxCaveatBytes int
	maxRevocations int

	clock uint64
	mu    sync.Mutex

	macCalls    atomic.Int64
	lookupCalls atomic.Int64
	heapPops    atomic.Int64

	revocations revocationTable
}

func NewService(rootKey []byte, mac MACFunc, maxCaveats, maxCaveatBytes, maxRevocations int) (*Service, error) {
	if len(rootKey) == 0 || mac == nil {
		return nil, &Error{Code: ErrInvalid}
	}
	if maxCaveats < 1 || maxCaveats > 32 || maxCaveatBytes < 1 || maxCaveatBytes > 256 ||
		maxRevocations < 1 || maxRevocations > 1_000_000 {
		return nil, &Error{Code: ErrInvalid}
	}

	service := &Service{
		rootKey:        bytes.Clone(rootKey),
		mac:            mac,
		maxCaveats:     maxCaveats,
		maxCaveatBytes: maxCaveatBytes,
		maxRevocations: maxRevocations,
		revocations:    newRevocationTable(),
	}
	return service, nil
}

func (s *Service) Mint(id []byte, caveats [][]byte, now uint64) (*Token, error) {
	if !validMintArguments(id, caveats, s.maxCaveats, s.maxCaveatBytes, now) {
		return nil, &Error{Code: ErrInvalid, MaxCaveats: s.maxCaveats}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return nil, &Error{Code: ErrClockRewound}
	}

	s.clock = now
	s.heapPops.Add(int64(s.revocations.expireAt(now)))

	signature := s.computeMAC(s.rootKey, id)
	for _, caveat := range caveats {
		signature = s.computeMAC(signature, caveat)
	}

	return &Token{
		ID:      bytes.Clone(id),
		Caveats: cloneByteSlices(caveats),
		Sig:     signature,
	}, nil
}

func Attenuate(service *Service, token *Token, caveat []byte) (*Token, error) {
	if !validAttenuateArguments(service, token, caveat) {
		return nil, &Error{Code: ErrInvalid}
	}
	if len(token.Caveats) >= service.maxCaveats {
		return nil, &Error{Code: ErrLimit, MaxCaveats: service.maxCaveats}
	}

	signature := service.computeMAC(bytes.Clone(token.Sig), caveat)
	return &Token{
		ID:      bytes.Clone(token.ID),
		Caveats: append(cloneByteSlices(token.Caveats), bytes.Clone(caveat)),
		Sig:     signature,
	}, nil
}

func (s *Service) Verify(token *Token, request Request, now uint64) error {
	if !validTokenShape(token, s.maxCaveats, s.maxCaveatBytes) || !validRequest(request) ||
		now > 1_000_000_000_000_000 {
		return &Error{Code: ErrInvalid}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return &Error{Code: ErrClockRewound}
	}

	s.clock = now
	s.heapPops.Add(int64(s.revocations.expireAt(now)))

	chain, validSignature := s.signatureChain(token)
	if !validSignature {
		return &Error{Code: ErrBadSignature}
	}

	id := string(token.ID)
	for index, signature := range chain {
		s.lookupCalls.Add(1)
		key := revocationKey{id: id, length: index, signature: string(signature)}
		if s.revocations.contains(key, now) {
			return &Error{Code: ErrRevoked, RevokedAt: index}
		}
	}

	for index, caveat := range token.Caveats {
		parsed := parseCaveat(caveat)
		switch {
		case !parsed.known:
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatUnknown}
		case !parsed.valid:
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatMalformed}
		case !caveatSatisfied(parsed, request, now):
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatUnsatisfied}
		}
	}

	return nil
}

func (s *Service) Revoke(token *Token, prefixLength int, now uint64) (*RevokeResult, error) {
	if !validTokenShape(token, s.maxCaveats, s.maxCaveatBytes) || prefixLength < 0 ||
		prefixLength > len(token.Caveats) || now > 1_000_000_000_000_000 {
		return nil, &Error{Code: ErrInvalid}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return nil, &Error{Code: ErrClockRewound}
	}

	chain, validSignature := s.signatureChain(token)
	if !validSignature {
		return nil, &Error{Code: ErrBadSignature}
	}

	s.clock = now
	s.heapPops.Add(int64(s.revocations.expireAt(now)))

	key := revocationKey{id: string(token.ID), length: prefixLength}
	key.signature = string(chain[prefixLength])
	existing := s.revocations.byKey[key]
	if recordActive(existing, now) {
		return &RevokeResult{}, nil
	}

	boundary, hasBoundary := prefixExpiryBoundary(token, prefixLength)
	if hasBoundary && boundary <= now {
		return &RevokeResult{Expired: true}, nil
	}

	if s.revocations.activeCount() == s.maxRevocations {
		return nil, &Error{Code: ErrLimit, MaxRecords: s.maxRevocations}
	}

	s.revocations.add(&revocationRecord{
		key:      key,
		boundary: boundary,
		infinite: !hasBoundary,
	})
	return &RevokeResult{}, nil
}

func (s *Service) MACCalls() int     { return int(s.macCalls.Load()) }
func (s *Service) LookupCalls() int  { return int(s.lookupCalls.Load()) }
func (s *Service) HeapPopCalls() int { return int(s.heapPops.Load()) }
func (s *Service) Clock() uint64     { s.mu.Lock(); defer s.mu.Unlock(); return s.clock }

func (s *Service) computeMAC(key, data []byte) []byte {
	s.macCalls.Add(1)
	result := s.mac(key, data)
	return bytes.Clone(result)
}

func (s *Service) signatureChain(token *Token) ([][]byte, bool) {
	chain := make([][]byte, 0, len(token.Caveats)+1)
	signature := s.computeMAC(s.rootKey, token.ID)
	chain = append(chain, signature)
	for _, caveat := range token.Caveats {
		signature = s.computeMAC(signature, caveat)
		chain = append(chain, signature)
	}
	return chain, bytes.Equal(signature, token.Sig)
}

func validMintArguments(id []byte, caveats [][]byte, maxCaveats, maxCaveatBytes int, now uint64) bool {
	if len(id) < 1 || len(id) > 64 || len(caveats) > maxCaveats || now > 1_000_000_000_000_000 {
		return false
	}
	for _, caveat := range caveats {
		if len(caveat) < 1 || len(caveat) > maxCaveatBytes {
			return false
		}
		parsed := parseCaveat(caveat)
		if !parsed.known || !parsed.valid {
			return false
		}
	}
	return true
}

func validAttenuateArguments(service *Service, token *Token, caveat []byte) bool {
	if service == nil || token == nil || len(token.ID) < 1 || len(token.ID) > 64 ||
		len(token.Sig) < 1 || len(token.Caveats) > service.maxCaveats ||
		len(caveat) < 1 || len(caveat) > service.maxCaveatBytes {
		return false
	}
	for _, existing := range token.Caveats {
		if len(existing) < 1 || len(existing) > service.maxCaveatBytes {
			return false
		}
	}
	return true
}

func cloneByteSlices(input [][]byte) [][]byte {
	if input == nil {
		return nil
	}
	output := make([][]byte, len(input))
	for index, value := range input {
		output[index] = bytes.Clone(value)
	}
	return output
}

func prefixExpiryBoundary(token *Token, prefixLength int) (uint64, bool) {
	var minimum uint64
	found := false
	for index := 0; index < prefixLength; index++ {
		parsed := parseCaveat(token.Caveats[index])
		if parsed.known && parsed.valid && parsed.prefix == "exp" {
			if !found || parsed.expiry < minimum {
				minimum = parsed.expiry
				found = true
			}
		}
	}
	return minimum, found
}
