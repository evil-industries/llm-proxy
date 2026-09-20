package managementevents

import (
	"sync"
	"testing"
)

func TestCoalescesWithoutBlockingAndUnsubscribes(t *testing.T) {
	first, stopFirst := Subscribe()
	second, stopSecond := Subscribe()
	defer stopSecond()
	var wg sync.WaitGroup
	for _, topic := range []Topic{Accounts, Config, Logs, DeviceAuth, RequestLogs} {
		wg.Go(func() {
			for range 1000 {
				Publish(topic)
			}
		})
	}
	wg.Wait()
	want := Accounts | Config | Logs | DeviceAuth | RequestLogs
	if got := <-first; got != want {
		t.Fatalf("coalesced topics = %d, want %d", got, want)
	}
	if got := <-second; got != want {
		t.Fatalf("second subscriber = %d, want %d", got, want)
	}
	stopFirst()
	stopFirst()
	Publish(Logs)
	select {
	case <-first:
		t.Fatal("unsubscribed listener received an event")
	default:
	}
	if got := <-second; got != Logs {
		t.Fatalf("active subscriber = %d", got)
	}
}
