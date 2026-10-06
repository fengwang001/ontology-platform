package parking

import "testing"

func TestConcurrentReservationsAndQueries(t *testing.T) {
	s, err := NewService(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 6)
	for i := int64(0); i < 6; i++ {
		index := i
		go func() {
			_, err := s.Reserve(0, ReservationRequest{
				ReservationID: "concurrent-" + string(rune('A'+index)),
				ZoneID:        "Z",
				Start:         Time(1000 + index*300),
				End:           Time(1100 + index*300),
				Vehicle:       string(rune('A' + index)),
			})
			done <- err
		}()
	}
	for i := 0; i < 6; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12; i++ {
		spot := []string{"N1", "N2", "C1"}[i%3]
		at := Time(1000 + (i%6)*300 + 50)
		go func() {
			_, err := s.SpotOccupantAt(spot, at)
			done <- err
		}()
	}
	for i := 0; i < 12; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
