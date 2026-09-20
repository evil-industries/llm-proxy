// Package managementevents broadcasts payload-free invalidations to console streams.
package managementevents

import "sync"

type Topic uint8

const (
	Accounts Topic = 1 << iota
	Config
	Logs
	RequestLogs
	DeviceAuth
)

var mu sync.Mutex
var subscribers = make(map[chan Topic]struct{})

// Subscribe uses a single coalescing slot, so slow consoles never block producers.
func Subscribe() (<-chan Topic, func()) {
	ch := make(chan Topic, 1)
	mu.Lock()
	subscribers[ch] = struct{}{}
	mu.Unlock()
	return ch, func() {
		mu.Lock()
		delete(subscribers, ch)
		mu.Unlock()
	}
}

func Publish(topic Topic) {
	mu.Lock()
	defer mu.Unlock()
	for ch := range subscribers {
		pending := topic
		select {
		case previous := <-ch:
			pending |= previous
		default:
		}
		ch <- pending
	}
}
