package ontology

import "bytes"

type naiveRecord struct {
	id        []byte
	length    int
	signature []byte
	boundary  uint64
	infinite  bool
}

type naiveService struct {
	rootKey        []byte
	mac            MACFunc
	maxCaveats     int
	maxCaveatBytes int
	maxRevocations int
	clock          uint64
	records        []naiveRecord
}

func newNaive(rootKey []byte, mac MACFunc, maxCaveats, maxCaveatBytes, maxRevocations int) *naiveService {
	return &naiveService{
		rootKey:        rootKey,
		mac:            mac,
		maxCaveats:     maxCaveats,
		maxCaveatBytes: maxCaveatBytes,
		maxRevocations: maxRevocations,
	}
}

func (s *naiveService) mint(id []byte, caveats [][]byte, now uint64) (*Token, *Error) {
	if !validMintArguments(id, caveats, s.maxCaveats, s.maxCaveatBytes, now) {
		return nil, &Error{Code: ErrInvalid, MaxCaveats: s.maxCaveats}
	}
	if now < s.clock {
		return nil, &Error{Code: ErrClockRewound}
	}
	s.clock = now
	s.gc(now)
	signature := s.mac(s.rootKey, id)
	for _, caveat := range caveats {
		signature = s.mac(signature, caveat)
	}
	return &Token{ID: bytes.Clone(id), Caveats: cloneByteSlices(caveats), Sig: signature}, nil
}

func (s *naiveService) verify(token *Token, request Request, now uint64) *Error {
	if !validTokenShape(token, s.maxCaveats, s.maxCaveatBytes) || !validRequest(request) ||
		now > 1_000_000_000_000_000 {
		return &Error{Code: ErrInvalid}
	}
	if now < s.clock {
		return &Error{Code: ErrClockRewound}
	}
	s.clock = now
	s.gc(now)

	chain := s.chain(token)
	if !bytes.Equal(chain[len(chain)-1], token.Sig) {
		return &Error{Code: ErrBadSignature}
	}

	for length, layer := range chain {
		for _, record := range s.records {
			if bytes.Equal(record.id, token.ID) && record.length == length &&
				bytes.Equal(record.signature, layer) {
				return &Error{Code: ErrRevoked, RevokedAt: length}
			}
		}
	}

	for index, caveat := range token.Caveats {
		parsed := parseCaveat(caveat)
		if !parsed.known {
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatUnknown}
		}
		if !parsed.valid {
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatMalformed}
		}
		if !caveatSatisfied(parsed, request, now) {
			return &Error{Code: ErrCaveat, Index: index + 1, Prefix: parsed.prefix, Kind: CaveatUnsatisfied}
		}
	}
	return nil
}

func (s *naiveService) revoke(token *Token, length int, now uint64) (bool, *Error) {
	if !validTokenShape(token, s.maxCaveats, s.maxCaveatBytes) || length < 0 ||
		length > len(token.Caveats) || now > 1_000_000_000_000_000 {
		return false, &Error{Code: ErrInvalid}
	}
	if now < s.clock {
		return false, &Error{Code: ErrClockRewound}
	}
	chain := s.chain(token)
	if !bytes.Equal(chain[len(chain)-1], token.Sig) {
		return false, &Error{Code: ErrBadSignature}
	}
	s.clock = now
	s.gc(now)
	for _, record := range s.records {
		if bytes.Equal(record.id, token.ID) && record.length == length &&
			bytes.Equal(record.signature, chain[length]) {
			return false, nil
		}
	}
	boundary, found := prefixExpiryBoundary(token, length)
	if found && boundary <= now {
		return true, nil
	}
	if len(s.records) == s.maxRevocations {
		return false, &Error{Code: ErrLimit, MaxRecords: s.maxRevocations}
	}
	s.records = append(s.records, naiveRecord{
		id:        bytes.Clone(token.ID),
		length:    length,
		signature: bytes.Clone(chain[length]),
		boundary:  boundary,
		infinite:  !found,
	})
	return false, nil
}

func (s *naiveService) chain(token *Token) [][]byte {
	chain := [][]byte{s.mac(s.rootKey, token.ID)}
	for _, caveat := range token.Caveats {
		chain = append(chain, s.mac(chain[len(chain)-1], caveat))
	}
	return chain
}

func (s *naiveService) gc(now uint64) {
	kept := s.records[:0]
	for _, record := range s.records {
		if record.infinite || record.boundary > now {
			kept = append(kept, record)
		}
	}
	s.records = kept
}
