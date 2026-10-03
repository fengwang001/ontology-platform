package sms

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

type naiveState struct {
	exists  bool
	balance int64
	period  int64
	used    int64
	first   int64
}

type naiveConfig struct {
	P    int64
	T0   int64
	P1   int64
	P2   int64
	MI   int
	max  int64
	seed int64
}

type naiveModel struct {
	config   naiveConfig
	accounts map[string]naiveState
}

func naiveUnits(r rune, encoding string) int {
	if encoding == "UCS2" {
		if r > 0xFFFF {
			return 2
		}
		return 1
	}
	if isExtendedRune(r) {
		return 2
	}
	return 1
}

func naiveAnalyze(text string) (string, []rune, int) {
	runes := []rune(text)
	encoding := "GSM"
	for _, r := range runes {
		if !isBasicRune(r) && !isExtendedRune(r) {
			encoding = "UCS2"
		}
	}
	total := 0
	for _, r := range runes {
		total += naiveUnits(r, encoding)
	}
	return encoding, runes, total
}

func naivePack(text string) (string, [][2]int, bool) {
	if text == "" {
		return "", nil, false
	}
	encoding, runes, total := naiveAnalyze(text)
	limit, part := gsmSingleLimit, gsmPartUnits
	if encoding == "UCS2" {
		limit, part = ucsSingleLimit, ucsPartUnits
	}
	if total <= limit {
		return encoding, [][2]int{{0, len(runes)}}, true
	}

	intervals := [][2]int{}
	start := 0
	used := 0
	for index, r := range runes {
		units := naiveUnits(r, encoding)
		if used+units > part {
			intervals = append(intervals, [2]int{start, index})
			start = index
			used = 0
		}
		used += units
	}
	intervals = append(intervals, [2]int{start, len(runes)})
	if len(intervals) > 10 {
		return encoding, intervals, false
	}
	return encoding, intervals, true
}

func (model *naiveModel) deposit(account string, amount int64) error {
	if account == "" || amount < 1 || amount > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	record := model.accounts[account]
	if !record.exists {
		record.exists = true
		record.first = model.config.T0
	}
	if record.balance > 1_000_000_000_000_000-amount {
		return ErrInvalidArgument
	}
	record.balance += amount
	model.accounts[account] = record
	return nil
}

func (model *naiveModel) send(account, text string, international bool, now int64, commit bool) (SendResult, error) {
	if account == "" {
		return SendResult{}, ErrInvalidArgument
	}
	if now < 0 || now > 1_000_000_000_000 {
		return SendResult{}, ErrInvalidArgument
	}
	if text == "" || !utf8.ValidString(text) {
		return SendResult{}, ErrInvalidArgument
	}
	record, exists := model.accounts[account]
	if !exists {
		return SendResult{}, ErrAccountNotFound
	}
	if now < model.config.max {
		return SendResult{}, ErrClockRolledBack
	}

	encoding, intervals, fits := naivePack(text)
	if !fits {
		return SendResult{}, ErrTooManySegments
	}

	currentPeriod := now / model.config.P
	current := record
	if currentPeriod != record.period {
		current.period = currentPeriod
		current.used = 0
		carry := int64(0)
		if currentPeriod == record.period+1 {
			carry = record.used
		}
		current.first = model.config.T0 + carry/4
	}

	segments := int64(len(intervals))
	domestic := int64(0)
	for index := int64(1); index <= segments; index++ {
		if current.used+index <= current.first {
			domestic += model.config.P1
		} else {
			domestic += model.config.P2
		}
	}
	cost := domestic
	if international {
		cost = (domestic*int64(model.config.MI) + 99) / 100
	}
	if cost > current.balance {
		return SendResult{}, ErrInsufficientFunds
	}
	result := SendResult{Encoding: encoding, Segments: len(intervals), Cost: cost}
	if commit {
		current.balance -= cost
		current.used += segments
		model.accounts[account] = current
		model.config.max = now
	}
	return result, nil
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	alphabet := []rune("ab12 .,!?{}[]~^|\\€{}}汉😀\uFFFD")

	for iteration := 1; iteration <= 2000; iteration++ {
		config := naiveConfig{
			P:    int64(rng.Intn(10) + 1),
			T0:   int64(rng.Intn(8)),
			P1:   int64(rng.Intn(12)),
			P2:   int64(rng.Intn(8)),
			MI:   100 + rng.Intn(201),
			seed: int64(iteration),
		}
		meter, err := New(config.P, config.T0, config.P1, config.P2, config.MI)
		if err != nil {
			t.Fatal(err)
		}
		model := &naiveModel{config: config, accounts: map[string]naiveState{}}
		names := []string{"", "a", "b"}

		t.Logf("[case %d] config=P%d T0=%d p1=%d p2=%d MI=%d", iteration, config.P, config.T0, config.P1, config.P2, config.MI)
		for operation := 0; operation < 28; operation++ {
			name := names[rng.Intn(len(names))]
			now := rng.Int63n(41)
			international := rng.Intn(2) == 0

			switch rng.Intn(10) {
			case 0:
				amount := int64(rng.Intn(80) + 1)
				t.Logf("[case %d op %d] Deposit(account=%q, amount=%d)", iteration, operation, name, amount)
				got := meter.Deposit(name, amount)
				want := model.deposit(name, amount)
				t.Logf("[case %d op %d] Deposit output=%v basis=%v", iteration, operation, got, want)
				if !sameError(got, want) {
					t.Fatalf("Deposit error mismatch: got %v want %v", got, want)
				}
			default:
				text := randomText(rng, alphabet, rng.Intn(180)+1)
				quote := rng.Intn(2) == 0
				method := "Send"
				var got SendResult
				var gotErr error
				if quote {
					method = "Quote"
					got, gotErr = meter.Quote(name, text, international, now)
				} else {
					got, gotErr = meter.Send(name, text, international, now)
				}
				_, intervals, _ := naivePack(text)
				want, wantErr := model.send(name, text, international, now, !quote)
				t.Logf("[case %d op %d] %s(account=%q, text=%q, international=%t, now=%d)", iteration, operation, method, name, text, international, now)
				t.Logf("[case %d op %d] output=%+v error=%v; basis=%+v error=%v; naive_segments=%d", iteration, operation, got, gotErr, want, wantErr, len(intervals))
				if !sameError(gotErr, wantErr) {
					t.Fatalf("%s error mismatch: got %v want %v, text=%q", method, gotErr, wantErr, text)
				}
				if gotErr == nil && got != want {
					t.Fatalf("%s result mismatch: got %+v want %+v, text=%q", method, got, want, text)
				}
			}
		}

		for name, want := range model.accounts {
			got := meter.accounts[name]
			if got == nil {
				t.Fatalf("case %d missing account %s", iteration, name)
			}
			if got.balance != want.balance || got.period != want.period || got.used != want.used || got.first != want.first || meter.maxNow != model.config.max {
				t.Fatalf("case %d state mismatch for %s: got balance=%d period=%d used=%d first=%d max=%d; want balance=%d period=%d used=%d first=%d max=%d",
					iteration, name, got.balance, got.period, got.used, got.first, meter.maxNow,
					want.balance, want.period, want.used, want.first, model.config.max)
			}
		}
	}
}

func randomText(rng *rand.Rand, alphabet []rune, maxRunes int) string {
	runes := make([]rune, 1+rng.Intn(maxRunes))
	for index := range runes {
		runes[index] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(runes)
}

func sameError(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return errors.Is(got, want)
}

func TestNaivePackMatchesSpecExamples(t *testing.T) {
	text := repeatRune('a', 152) + "{" + repeatRune('b', 10)
	encoding, intervals, ok := naivePack(text)
	if encoding != "GSM" || !ok || fmt.Sprint(intervals) != "[[0 152] [152 163]]" {
		t.Fatalf("GSM naive = %s %v %v", encoding, intervals, ok)
	}
	ucs := strings.Repeat("汉", 66) + "😀" + strings.Repeat("汉", 5)
	encoding, intervals, ok = naivePack(ucs)
	if encoding != "UCS2" || !ok || fmt.Sprint(intervals) != "[[0 66] [66 72]]" {
		t.Fatalf("UCS2 naive = %s %v %v", encoding, intervals, ok)
	}
}
