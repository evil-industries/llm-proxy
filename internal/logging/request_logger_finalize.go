package logging

import "sync"

// Limit retained request state and concurrent final assembly/fsync/Home work.
// Callers retain the synchronous, lossless path when every slot is occupied.
var requestLogFinalizers = struct {
	slots chan struct{}
	sync.WaitGroup
}{slots: make(chan struct{}, 8)}

// TryFinalizeRequestLog transfers ownership of work to a bounded background task.
// A false return means the caller still owns the work and must run it itself.
func TryFinalizeRequestLog(work func()) bool {
	select {
	case requestLogFinalizers.slots <- struct{}{}:
		requestLogFinalizers.Add(1)
		go func() {
			defer requestLogFinalizers.Done()
			defer func() { <-requestLogFinalizers.slots }()
			work()
		}()
		return true
	default:
		return false
	}
}

// WaitForRequestLogs drains accepted work after HTTP handlers have stopped.
func WaitForRequestLogs() { requestLogFinalizers.Wait() }
