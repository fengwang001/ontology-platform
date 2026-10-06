package certselector

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type operationLog struct {
	kind   string
	input  string
	actual string
	naive  string
	reason string
}

func randomDomain(random *rand.Rand, wildcard bool) string {
	parts := []string{
		[]string{"api", "www", "edge", "db", "a", "v2", "admin"}[random.Intn(7)],
		[]string{"example", "corp", "test", "site"}[random.Intn(4)],
		[]string{"com", "net", "org", "io"}[random.Intn(4)],
	}
	name := parts[random.Intn(2)] + "." + parts[2]
	if random.Intn(3) == 0 {
		name = parts[0] + "." + name
	}
	if wildcard {
		return "*." + name
	}
	return name
}

func randomCertificate(random *rand.Rand, sequence int) Certificate {
	nameCount := 1 + random.Intn(3)
	names := make([]string, 0, nameCount)
	for i := 0; i < nameCount; i++ {
		names = append(names, randomDomain(random, random.Intn(2) == 0))
	}
	keyType := KeyTypeEC
	if random.Intn(2) == 0 {
		keyType = KeyTypeRSA
	}
	notBefore := int64(random.Intn(20))
	notAfter := notBefore + int64(1+random.Intn(20))
	return Certificate{
		ID:        fmt.Sprintf("c-%03d-%02d", sequence, random.Intn(12)),
		Names:     names,
		KeyType:   keyType,
		NotBefore: notBefore,
		NotAfter:  notAfter,
	}
}

func formatKeyTypes(types map[KeyType]bool) string {
	result := ""
	if types[KeyTypeEC] {
		result += "EC"
	}
	if types[KeyTypeRSA] {
		if result != "" {
			result += "+"
		}
		result += "RSA"
	}
	if result == "" {
		return "none"
	}
	return result
}

func errorString(err error) string {
	if err == nil {
		return "ok"
	}
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, ErrCertificateConflict):
		return "conflict"
	case errors.Is(err, ErrCertificateNotFound):
		return "not-found"
	case errors.Is(err, ErrNoMatchingCertificate):
		return "no-match"
	case errors.Is(err, ErrNoValidCertificate):
		return "not-valid"
	case errors.Is(err, ErrUnsupportedKeyType):
		return "unsupported-key-type"
	default:
		return err.Error()
	}
}

func TestRandomizedModelComparison(t *testing.T) {
	random := rand.New(rand.NewSource(1584))

	for sequence := 0; sequence < 1200; sequence++ {
		selector := New()
		model := newNaiveModel()
		logs := make([]operationLog, 0, 18)

		for step := 0; step < 18; step++ {
			switch random.Intn(5) {
			case 0:
				certificate := randomCertificate(random, sequence)
				if random.Intn(10) == 0 {
					certificate.Names = nil
				}
				actualErr := selector.Add(certificate)
				_, naiveErr := model.add(certificate)
				logs = append(logs, operationLog{
					kind:   "add",
					input:  fmt.Sprintf("id=%s names=%v key=%d [%d,%d)", certificate.ID, certificate.Names, certificate.KeyType, certificate.NotBefore, certificate.NotAfter),
					actual: errorString(actualErr),
					naive:  errorString(naiveErr),
					reason: "reject invalid or duplicate; otherwise index every SAN",
				})

			case 1:
				id := fmt.Sprintf("c-%03d-%02d", sequence, random.Intn(12))
				if random.Intn(8) == 0 {
					id = ""
				}
				actualErr := selector.Remove(id)
				_, naiveErr := model.remove(id)
				logs = append(logs, operationLog{
					kind:   "remove",
					input:  "id=" + id,
					actual: errorString(actualErr),
					naive:  errorString(naiveErr),
					reason: "missing certificate is rejected; removing default clears it",
				})

			case 2:
				id := fmt.Sprintf("c-%03d-%02d", sequence, random.Intn(12))
				if random.Intn(8) == 0 {
					id = ""
				}
				actualErr := selector.SetDefault(id)
				_, naiveErr := model.setDefault(id)
				logs = append(logs, operationLog{
					kind:   "set-default",
					input:  "id=" + id,
					actual: errorString(actualErr),
					naive:  errorString(naiveErr),
					reason: "default must refer to an existing certificate",
				})

			case 3:
				selector.RemoveDefault()
				reason := model.clearDefault()
				logs = append(logs, operationLog{
					kind:   "clear-default",
					input:  "-",
					actual: "ok",
					naive:  "ok",
					reason: reason,
				})

			default:
				input := SelectInput{Now: int64(random.Intn(45)), KeyTypes: map[KeyType]bool{}}
				if random.Intn(12) != 0 {
					input.KeyTypes[KeyTypeEC] = random.Intn(2) == 0
					input.KeyTypes[KeyTypeRSA] = random.Intn(2) == 0
				}
				switch random.Intn(8) {
				case 0:
					input.Name = ""
				case 1:
					input.Name = "bad..name"
				case 2:
					input.Name = "trailing.example."
				case 3:
					input.Name = "UPPER.Example.COM"
				default:
					input.Name = randomDomain(random, false)
				}

				actual, actualErr := selector.Select(input)
				naive, reason := model.selectCertificate(input)
				actualResult := errorString(actualErr)
				if actualErr == nil {
					actualResult = actual.Certificate.ID
				}
				naiveResult := errorString(naive.err)
				if naive.err == nil {
					naiveResult = naive.id
				}
				logs = append(logs, operationLog{
					kind:   "select",
					input:  fmt.Sprintf("name=%q keys=%s now=%d", input.Name, formatKeyTypes(input.KeyTypes), input.Now),
					actual: fmt.Sprintf("%s/source=%d", actualResult, actual.Source),
					naive:  fmt.Sprintf("%s/source=%d", naiveResult, naive.source),
					reason: reason,
				})
			}

			last := logs[len(logs)-1]
			if last.actual != last.naive {
				for index, log := range logs {
					t.Logf("sequence=%04d step=%02d kind=%s input=%s actual=%s naive=%s reason=%s", sequence, index, log.kind, log.input, log.actual, log.naive, log.reason)
				}
				t.Fatalf("sequence %d mismatch: actual %s, naive %s", sequence, last.actual, last.naive)
			}
			t.Logf("sequence=%04d step=%02d kind=%s input=%s output=%s reason=%s", sequence, step, last.kind, last.input, last.actual, last.reason)
		}
	}
}
