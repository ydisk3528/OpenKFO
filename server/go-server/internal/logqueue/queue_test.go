package logqueue

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBlockedSinkDoesNotBlockProducer(t *testing.T) {
	var out bytes.Buffer
	q := New(&out, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	q.Submit(func() { close(entered); <-release })
	<-entered
	p := []byte("original\n")
	q.Write(p)
	p[0] = 'X'
	finished := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			q.Write([]byte("overflow\n"))
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("producer blocked")
	}
	if q.Close(time.Millisecond) {
		t.Fatal("stuck sink reported drained")
	}
	close(release)
	if !q.Close(time.Second) {
		t.Fatal("did not drain")
	}
	if !strings.Contains(out.String(), "original\n") || !strings.Contains(out.String(), "count=10000") {
		t.Fatal(out.String())
	}
	if q.Submit(func() { t.Error("closed queue ran job") }) {
		t.Fatal("accepted after close")
	}
}
