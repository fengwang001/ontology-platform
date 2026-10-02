package ontology

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

type randomToken struct {
	token   *Token
	caveats [][]byte
}

func TestRandomDifferential(t *testing.T) {
	source := rand.New(rand.NewSource(20261002))
	operations := 2000
	logs := make([]string, 0, operations)
	service, _ := NewService([]byte("deterministic-root"), testMAC, 8, 64, 5)
	naive := newNaive([]byte("deterministic-root"), testMAC, 8, 64, 5)
	tokens := make([]randomToken, 0, 64)

	addToken := func(step int, kind string, real *Token, model *Token, log string) {
		if !tokensEqual(real, model) {
			logs = append(logs, log)
			t.Fatalf("step %d %s token mismatch\n%s\nreal=%x caveats=%q\nmodel=%x caveats=%q",
				step, kind, joinLogs(logs), real.Sig, real.Caveats, model.Sig, model.Caveats)
		}
		tokens = append(tokens, randomToken{token: real, caveats: cloneByteSlices(real.Caveats)})
	}

	for step := 0; step < operations; step++ {
		now := uint64(source.Intn(30))
		choice := source.Intn(10)
		switch {
		case choice < 3:
			caveats := randomCaveats(source)
			id := []byte(fmt.Sprintf("id-%d", source.Intn(8)))
			real, realMintErr := service.Mint(bytes.Clone(id), cloneByteSlices(caveats), now)
			model, modelErr := naive.mint(bytes.Clone(id), cloneByteSlices(caveats), now)
			var realErr error
			if realMintErr != nil {
				realErr = realMintErr
			}
			var modelResultErr error
			if modelErr != nil {
				modelResultErr = modelErr
			}
			log := fmt.Sprintf("step=%d op=Mint id=%q caveats=%q now=%d real=%v model=%v",
				step, id, caveats, now, realErr, modelResultErr)
			logs = append(logs, log)
			assertSameError(t, step, logs, realErr, modelResultErr)
			if realErr == nil {
				addToken(step, "Mint", real, model, log)
			}
		case choice < 6 && len(tokens) > 0:
			entry := tokens[source.Intn(len(tokens))]
			caveat := randomAttenuationCaveat(source)
			real, realErr := Attenuate(service, entry.token, bytes.Clone(caveat))
			model := attenuateNaive(naive, entry.token, caveat)
			log := fmt.Sprintf("step=%d op=Attenuate id=%q caveat=%q real=%v",
				step, entry.token.ID, caveat, realErr)
			logs = append(logs, log)
			if (realErr == nil) != (model != nil) {
				t.Fatalf("step %d Attenuate result mismatch\n%s\nreal=%v modelNil=%v",
					step, joinLogs(logs), realErr, model == nil)
			}
			if realErr == nil {
				addToken(step, "Attenuate", real, model, log)
			}
		case choice < 8 && len(tokens) > 0:
			entry := tokens[source.Intn(len(tokens))]
			length := source.Intn(len(entry.caveats) + 1)
			if source.Intn(8) == 0 {
				length = len(entry.caveats) + 1
			}
			realResult, realRevokeErr := service.Revoke(entry.token, length, now)
			modelExpired, modelModelErr := naive.revoke(entry.token, length, now)
			var realErr error
			if realRevokeErr != nil {
				realErr = realRevokeErr
			}
			var modelErr error
			if modelModelErr != nil {
				modelErr = modelModelErr
			}
			log := fmt.Sprintf("step=%d op=Revoke id=%q length=%d now=%d real=(%#v,%v) model=(expired=%v,%v)",
				step, entry.token.ID, length, now, realResult, realErr, modelExpired, modelErr)
			logs = append(logs, log)
			assertSameError(t, step, logs, realErr, modelErr)
			if realErr == nil && realResult.Expired != modelExpired {
				t.Fatalf("step %d expiry marker mismatch\n%s", step, joinLogs(logs))
			}
		case len(tokens) > 0:
			entry := tokens[source.Intn(len(tokens))]
			request := Request{
				Op:     []string{"read", "write", "bad-op"}[source.Intn(3)],
				Res:    []string{"/", "/a", "/a/b", "/ab", "bad"}[source.Intn(5)],
				Amount: uint64(source.Intn(12)),
			}
			realVerifyErr := service.Verify(entry.token, request, now)
			modelVerifyErr := naive.verify(entry.token, request, now)
			var realErr error
			if realVerifyErr != nil {
				realErr = realVerifyErr
			}
			var modelErr error
			if modelVerifyErr != nil {
				modelErr = modelVerifyErr
			}
			log := fmt.Sprintf("step=%d op=Verify id=%q caveats=%q request=%+v now=%d real=%v model=%v",
				step, entry.token.ID, entry.caveats, request, now, realErr, modelErr)
			logs = append(logs, log)
			assertSameError(t, step, logs, realErr, modelErr)
		default:
			logs = append(logs, fmt.Sprintf("step=%d op=Idle now=%d", step, now))
		}

		if service.Clock() != naive.clock || service.revocations.activeCount() != len(naive.records) {
			t.Fatalf("step %d state mismatch clock=(%d,%d) records=(%d,%d)\n%s",
				step, service.Clock(), naive.clock, service.revocations.activeCount(), len(naive.records),
				joinLogs(logs))
		}
	}

	if testing.Verbose() {
		for _, log := range logs {
			t.Log(log)
		}
	}
}

func randomCaveats(source *rand.Rand) [][]byte {
	caveats := make([][]byte, source.Intn(4))
	for index := range caveats {
		caveats[index] = randomMintCaveat(source)
	}
	return caveats
}

func randomMintCaveat(source *rand.Rand) []byte {
	switch source.Intn(4) {
	case 0:
		return []byte(fmt.Sprintf("exp:%d", source.Intn(30)))
	case 1:
		return []byte(fmt.Sprintf("ops:%s", []string{"read", "write", "read,write"}[source.Intn(3)]))
	case 2:
		return []byte(fmt.Sprintf("res:%s", []string{"/", "/a", "/b", "/a/b"}[source.Intn(4)]))
	default:
		return []byte(fmt.Sprintf("amt:%d", source.Intn(10)))
	}
}

func randomAttenuationCaveat(source *rand.Rand) []byte {
	if source.Intn(10) == 0 {
		return [][]byte{[]byte("unknown:x"), []byte("exp:bad"), []byte("res:bad")}[source.Intn(3)]
	}
	return randomMintCaveat(source)
}

func attenuateNaive(service *naiveService, token *Token, caveat []byte) *Token {
	shape := &Service{maxCaveats: service.maxCaveats, maxCaveatBytes: service.maxCaveatBytes}
	if !validAttenuateArguments(shape, token, caveat) || len(token.Caveats) >= service.maxCaveats {
		return nil
	}
	signature := service.mac(bytes.Clone(token.Sig), caveat)
	return &Token{
		ID:      bytes.Clone(token.ID),
		Caveats: append(cloneByteSlices(token.Caveats), bytes.Clone(caveat)),
		Sig:     signature,
	}
}

func tokensEqual(left, right *Token) bool {
	return bytes.Equal(left.ID, right.ID) && bytes.Equal(left.Sig, right.Sig) &&
		bytes.Equal(bytes.Join(left.Caveats, []byte{0}), bytes.Join(right.Caveats, []byte{0}))
}

func assertSameError(t *testing.T, step int, logs []string, real, model error) {
	t.Helper()
	if real == nil && model == nil {
		return
	}
	if real == nil || model == nil {
		t.Fatalf("step %d error mismatch real=%v model=%v\n%s", step, real, model, joinLogs(logs))
	}
	left := real.(*Error)
	right := model.(*Error)
	if left.Code != right.Code || left.Index != right.Index || left.Kind != right.Kind ||
		left.RevokedAt != right.RevokedAt {
		t.Fatalf("step %d error detail mismatch real=%#v model=%#v\n%s",
			step, left, right, joinLogs(logs))
	}
}

func joinLogs(logs []string) string {
	combined := ""
	for _, log := range logs {
		combined += log + "\n"
	}
	return combined
}
