package logqueue

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type recoveringWriter struct {
	bytes.Buffer
	fail bool
}

func (w *recoveringWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(p)
}
func (w *recoveringWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func TestWriteFailureCountersSurviveFailedReport(t *testing.T) {
	w := &recoveringWriter{fail: true}
	q := &Queue{out: w, jobs: make(chan func(), 1)}
	q.Write([]byte("lost record"))
	(<-q.jobs)()
	q.dropped.Store(3)
	q.report()
	if q.writeErrors.Load() != 1 || q.dropped.Load() != 3 {
		t.Fatal("lost failure counters")
	}
	w.fail = false
	q.report()
	if !strings.Contains(w.String(), "count=3 write_errors=1") || q.writeErrors.Load() != 0 || q.dropped.Load() != 0 {
		t.Fatal(w.String())
	}
}
