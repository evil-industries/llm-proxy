package logging

import "testing"

func TestRequestLogFinalizersBoundOutstandingWork(t *testing.T) {
	WaitForRequestLogs()
	release := make(chan struct{})
	t.Cleanup(func() { close(release); WaitForRequestLogs() })
	for i := 0; i < cap(requestLogFinalizers.slots); i++ {
		if !TryFinalizeRequestLog(func() { <-release }) {
			t.Fatalf("background task %d was unexpectedly rejected", i)
		}
	}
	if TryFinalizeRequestLog(func() { t.Error("rejected task ran") }) {
		t.Fatal("finalization retained more than its bounded number of requests")
	}
}
