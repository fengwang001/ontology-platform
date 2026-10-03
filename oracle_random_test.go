package ontology

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRandomSequencesAgainstNaiveOracle(t *testing.T) {
	kinds := []ReceiptKind{Sent, Delivered, Read, Soft, Hard}

	for i := range 2000 {
		rng := rand.New(rand.NewPCG(uint64(i+1), uint64(9001-i)))
		softLimit := rng.IntN(4) + 1
		tracker := mustNewTracker(t, softLimit)
		oracle := newOracleTracker(softLimit)
		acceptedReceipts := 0
		resultCounts := map[ReceiptResult]int{}

		var messages []struct {
			name     string
			rcpts    []string
			deadline int64
		}
		for msgIndex := range rng.IntN(2) + 1 {
			var rcpts []string
			for rcptIndex := range rng.IntN(3) + 1 {
				rcpts = append(rcpts, fmt.Sprintf("r%d", rcptIndex))
			}
			msg := struct {
				name     string
				rcpts    []string
				deadline int64
			}{
				name:     fmt.Sprintf("m%d", msgIndex),
				rcpts:    rcpts,
				deadline: int64(rng.IntN(11)),
			}
			messages = append(messages, msg)

			rcptBytes := make([][]byte, len(rcpts))
			for index, rcpt := range rcpts {
				rcptBytes[index] = []byte(rcpt)
			}
			t.Logf("case=%d input=Send msg=%q rcpts=%s deadline=%d output=%v basis=valid unique registration S=%d",
				i, msg.name, strings.Join(rcpts, ","), msg.deadline, nil, softLimit)
			if err := tracker.Send([]byte(msg.name), rcptBytes, msg.deadline); err != nil {
				t.Fatalf("case %d Send: %v", i, err)
			}
			if err := oracle.send(msg.name, rcpts, msg.deadline); err != nil {
				t.Fatalf("case %d oracle Send: %v", i, err)
			}
		}

		lastTick := int64(0)
		for range rng.IntN(10) + 4 {
			msg := messages[rng.IntN(len(messages))]
			switch rng.IntN(5) {
			case 0:
				lastTick += int64(rng.IntN(3))
				t.Logf("case=%d input=Tick now=%d", i, lastTick)
				prodTick, err := tracker.Tick(lastTick)
				if err != nil {
					t.Fatalf("case %d Tick: %v", i, err)
				}
				oracleTick := oracle.tick(lastTick)
				t.Logf("case=%d output=Tick expirations=%v basis=deadline <= now and pending r<2", i, prodTick)
				if !slices.EqualFunc(prodTick, oracleTick, expirationEqual) {
					t.Fatalf("case %d Tick mismatch: product=%v oracle=%v", i, prodTick, oracleTick)
				}
			case 1:
				t.Logf("case=%d input=Status msg=%q", i, msg.name)
				prodStatus, err := tracker.Status([]byte(msg.name))
				if err != nil {
					t.Fatalf("case %d Status: %v", i, err)
				}
				oracleSnap := oracle.snapshot(msg.name)
				prodSnap := productionSnapshot(tracker, msg.name)
				oracleAggregate := aggregateFromSnapshot(oracleSnap)
				t.Logf("case=%d output=Status status=%s recipients=%v basis=product snapshot=%v oracle aggregate=%s",
					i, prodStatus.Status, prodStatus.Recipients, prodSnap, oracleAggregate)
				if !reflect.DeepEqual(prodSnap, oracleSnap) || prodStatus.Status != oracleAggregate {
					t.Fatalf("case %d Status mismatch: product=%+v productSnap=%v oracleSnap=%v oracleAggregate=%s",
						i, prodStatus, prodSnap, oracleSnap, oracleAggregate)
				}
			default:
				rcpt := msg.rcpts[rng.IntN(len(msg.rcpts))]
				kind := kinds[rng.IntN(len(kinds))]
				attempt := rng.IntN(5) + 1
				ts := int64(rng.IntN(13))
				prodResult, err := tracker.Receipt([]byte(msg.name), []byte(rcpt), kind, attempt, ts)
				if err != nil {
					t.Fatalf("case %d Receipt: %v", i, err)
				}
				oracleResult := oracle.receipt(msg.name, rcpt, kind, attempt, ts)
				acceptedReceipts++
				resultCounts[prodResult]++
				t.Logf("case=%d input=Receipt msg=%q rcpt=%q kind=%s attempt=%d ts=%d output=%s oracle=%s basis=%s",
					i, msg.name, rcpt, kind, attempt, ts, prodResult, oracleResult, decisionBasis(prodResult))
				if prodResult != oracleResult {
					t.Fatalf("case %d Receipt mismatch: product=%s oracle=%s", i, prodResult, oracleResult)
				}
			}
		}

		for _, msg := range messages {
			prodSnap := productionSnapshot(tracker, msg.name)
			oracleSnap := oracle.snapshot(msg.name)
			t.Logf("case=%d input=finalStatus msg=%q output=%v basis=full state equality including r fail sentA late softSet seen",
				i, msg.name, prodSnap)
			if !reflect.DeepEqual(prodSnap, oracleSnap) {
				t.Fatalf("case %d final snapshot mismatch for %q: product=%v oracle=%v", i, msg.name, prodSnap, oracleSnap)
			}
		}
		countSum := resultCounts[Applied] + resultCounts[Stale] + resultCounts[Duplicate] + resultCounts[Ignored]
		t.Logf("case=%d output=receiptCounts accepted=%d applied=%d stale=%d duplicate=%d ignored=%d sum=%d basis=every accepted receipt has exactly one disposition",
			i, acceptedReceipts, resultCounts[Applied], resultCounts[Stale], resultCounts[Duplicate], resultCounts[Ignored], countSum)
		if countSum != acceptedReceipts {
			t.Fatalf("case %d result count sum=%d accepted=%d", i, countSum, acceptedReceipts)
		}
	}
}

func aggregateFromSnapshot(snapshots []oracleSnapshot) AggregateStatus {
	failedCount := 0
	pending := 0
	allRead := true
	for _, snapshot := range snapshots {
		if snapshot.fail != NoFailure {
			failedCount++
		}
		if snapshot.fail == NoFailure && snapshot.r < 2 {
			pending++
		}
		if snapshot.r != 3 {
			allRead = false
		}
	}

	switch {
	case failedCount == len(snapshots):
		return Failed
	case pending > 0:
		return Inflight
	case failedCount > 0:
		return Partial
	case allRead:
		return ReadStatus
	default:
		return DeliveredStatus
	}
}
