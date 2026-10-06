package aml

import (
	"fmt"
	"reflect"
	"testing"
)

func TestLinkIgnoresLargerGroupHistoryOutsideWindow(t *testing.T) {
	system, err := NewSystem(Config{L: 100, H: 300, K: 3, D: 2})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}

	mustOpen(t, system, 1, "large")
	mustOpen(t, system, 1, "small-a")
	mustOpen(t, system, 1, "small-b")

	makeDeposit(t, system, 1, "large", "large-old-a", 100)
	makeDeposit(t, system, 1, "large", "large-old-b", 100)

	makeDeposit(t, system, 2, "small-a", "small-a-1", 100)
	makeDeposit(t, system, 3, "small-b", "small-b-1", 100)
	makeDeposit(t, system, 3, "small-b", "small-b-2", 100)

	report, err := system.Link(3, "small-a", "small-b")
	if err != nil {
		t.Fatalf("Link(small groups) error = %v", err)
	}
	if report == nil {
		t.Fatal("expected small-group link report")
	}

	report, err = system.Link(3, "large", "small-a")
	if err != nil {
		t.Fatalf("Link(large group) error = %v", err)
	}
	mergedGroup := system.groups[system.accounts["large"].groupID]
	if _, exists := mergedGroup.buckets[1]; exists {
		t.Fatal("merge retained larger group's day-1 bucket outside the current D=2 window")
	}
	if report != nil {
		t.Fatalf("larger group's day-1 history must not be considered: %+v", report)
	}
	accounts, err := system.GroupAccounts(3, "large")
	if err != nil {
		t.Fatalf("GroupAccounts() error = %v", err)
	}
	if want := []string{"large", "small-a", "small-b"}; !reflect.DeepEqual(accounts, want) {
		t.Fatalf("accounts = %v, want %v", accounts, want)
	}
}

func BenchmarkLinkSmallGroupToLargeGroupWithOldHistory(b *testing.B) {
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		system := testSystem(b)
		mustOpen(b, system, 1, "large")
		mustOpen(b, system, 1, "small-a")
		mustOpen(b, system, 1, "small-b")
		for i := 0; i < 1024; i++ {
			if _, err := system.Deposit(1, "large", fmt.Sprintf("large-old-%d", i), 100); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := system.Deposit(6, "small-a", "small-a-1", 100); err != nil {
			b.Fatal(err)
		}
		if _, err := system.Deposit(6, "small-b", "small-b-1", 100); err != nil {
			b.Fatal(err)
		}
		if _, err := system.Deposit(6, "small-b", "small-b-2", 100); err != nil {
			b.Fatal(err)
		}
		if _, err := system.Link(6, "small-a", "small-b"); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		system.Link(6, "large", "small-a")
	}
}
