// Package logqueue keeps slow diagnostic sinks off the gameplay goroutine.
package logqueue

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Queue struct {
	mu          sync.Mutex
	closed      bool
	jobs        chan func()
	done        chan struct{}
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	out         io.Writer
}

func New(out io.Writer, capacity int) *Queue {
	q := &Queue{jobs: make(chan func(), capacity), done: make(chan struct{}), out: out}
	go q.run()
	return q
}

// Submit never waits for disk or queue capacity. Overflow is counted and reported.
func (q *Queue) Submit(job func()) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		select {
		case q.jobs <- job:
			return true
		default:
		}
	}
	q.dropped.Add(1)
	return false
}

func (q *Queue) Write(p []byte) (int, error) {
	if len(p) > 256<<10 {
		q.dropped.Add(1)
		return len(p), nil
	}
	copy := append([]byte(nil), p...)
	q.Submit(func() {
		n, err := q.out.Write(copy)
		if err != nil || n != len(copy) {
			q.writeErrors.Add(1)
		}
	})
	return len(p), nil
}

func (q *Queue) report() {
	n, failures := q.dropped.Load(), q.writeErrors.Load()
	if n > 0 || failures > 0 {
		line := fmt.Sprintf("diagnostic_queue_dropped count=%d write_errors=%d gameplay_not_blocked=true\n", n, failures)
		written, err := io.WriteString(q.out, line)
		if err == nil && written == len(line) {
			q.dropped.Add(-n)
			q.writeErrors.Add(-failures)
		}
	}
}

func (q *Queue) run() {
	defer close(q.done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case job, ok := <-q.jobs:
			if !ok {
				q.report()
				return
			}
			job()
		case <-ticker.C:
			q.report()
		}
	}
}

// Close drains accepted jobs, but a stuck sink cannot hold shutdown indefinitely.
func (q *Queue) Close(timeout time.Duration) bool {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.jobs)
	}
	q.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-q.done:
		return true
	case <-timer.C:
		return false
	}
}
