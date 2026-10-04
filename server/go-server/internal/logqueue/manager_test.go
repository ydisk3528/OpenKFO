package logqueue

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerBoundsAndJSON(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, Limits{1024, 3, 100, 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		m.Write([]byte("example \"event\"\n"))
	}
	m.Write(bytes.Repeat([]byte("x"), 101))
	if !m.Close(time.Second) {
		t.Fatal("close timeout")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) > 3 || len(files) < 2 {
		t.Fatal(files)
	}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil || len(b) > 1024 {
			t.Fatal(p, len(b), e)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
			if !json.Valid(line) {
				t.Fatal(string(line))
			}
		}
	}
	if m.Submit(func() { t.Error("accepted after close") }) {
		t.Fatal("closed")
	}
}
func TestManagerDiskFailureBackoff(t *testing.T) {
	s := &jsonSink{directory: filepath.Join(t.TempDir(), "missing"), limits: Limits{1024, 1, 1, 100}}
	if _, err := s.Write([]byte("test")); err == nil {
		t.Fatal("expected failure")
	}
	if !s.retryAfter.After(time.Now()) {
		t.Fatal("missing cooldown")
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("test")); err == nil {
		t.Fatal("retried during cooldown")
	}
	s.retryAfter = time.Time{}
	if _, err := s.Write([]byte("recovered")); err != nil {
		t.Fatal(err)
	}
	s.file.Close()
}

type stalledConsole struct{ entered, release chan struct{} }

func (w *stalledConsole) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(p), nil
}
func TestManagerStalledConsoleDoesNotBlockGame(t *testing.T) {
	w := &stalledConsole{make(chan struct{}, 1), make(chan struct{})}
	m, err := NewManager(t.TempDir(), Limits{1024, 2, 1, 100}, w)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(w.release); m.Close(time.Second) }()
	m.Write([]byte("first"))
	<-w.entered
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			m.Write([]byte("sample"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("producer blocked")
	}
	if m.Close(time.Millisecond) {
		t.Fatal("unexpected drain")
	}
	if m.dropped.Load() == 0 {
		t.Fatal("missing overflow count")
	}
}
