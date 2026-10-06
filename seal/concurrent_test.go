package seal

import "testing"

func TestConcurrentReplayDeterminism(t *testing.T) {
	first := concurrentScenario(t)
	second := concurrentScenario(t)
	if first != second {
		t.Fatalf("replay result changed: %q then %q", first, second)
	}
}

func concurrentScenario(t *testing.T) ApplicationState {
	t.Helper()
	service := setupService(t)
	submit(t, service, "app", 20, 50, "contract")
	approve(t, service, "app", 21, "a1")
	done := make(chan error, 64)
	for range 64 {
		go func() {
			done <- service.ExecuteApplication(ApplicationInput{Now: 22, ApplicationID: "app"})
		}()
	}
	var success int
	for range 64 {
		if err := <-done; err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successful executions = %d, want 1", success)
	}
	application, _ := service.GetApplication("app")
	return application.State
}
