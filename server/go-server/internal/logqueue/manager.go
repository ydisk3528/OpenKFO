package logqueue

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager is for disposable diagnostics, never wallet or item transaction records.
// Encoding, disk writes, rotation and console output all run on Queue's worker.
type Manager struct {
	*Queue
	sink      *jsonSink
	closeOnce sync.Once
}
type Limits struct {
	FileBytes                 int64
	Files, Queue, RecordBytes int
}

func DefaultLimits() Limits { return Limits{20 << 20, 10, 1024, 16 << 10} }

func NewManager(directory string, limits Limits, console io.Writer) (*Manager, error) {
	if directory == "" || limits.FileBytes < 1024 || limits.Files < 1 || limits.Queue < 1 || limits.RecordBytes < 1 || int64(limits.RecordBytes)*6+128 > limits.FileBytes {
		return nil, fmt.Errorf("invalid diagnostic log limits")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	s := &jsonSink{directory: directory, limits: limits, console: console}
	if err := s.open(); err != nil {
		return nil, err
	}
	return &Manager{Queue: New(s, limits.Queue), sink: s}, nil
}
func (m *Manager) Write(p []byte) (int, error) {
	if len(p) > m.sink.limits.RecordBytes {
		m.dropped.Add(1)
		return len(p), nil
	}
	return m.Queue.Write(p)
}
func (m *Manager) Close(timeout time.Duration) bool {
	if !m.Queue.Close(timeout) {
		return false
	}
	// Only close the owned file after all accepted jobs and summaries have drained.
	m.closeOnce.Do(func() {
		if m.sink.file != nil {
			m.sink.file.Close()
		}
	})
	return true
}

type jsonSink struct {
	directory  string
	limits     Limits
	console    io.Writer
	file       *os.File
	size       int64
	retryAfter time.Time
}

func (s *jsonSink) path(i int) string {
	if i == 0 {
		return filepath.Join(s.directory, "diagnostic.jsonl")
	}
	return filepath.Join(s.directory, fmt.Sprintf("diagnostic.%d.jsonl", i))
}
func (s *jsonSink) open() error {
	f, err := os.OpenFile(s.path(0), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	s.file = f
	s.size = stat.Size()
	return nil
}
func (s *jsonSink) rotate() error {
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			s.file = nil
			return err
		}
		s.file = nil
	}
	if err := os.Remove(s.path(s.limits.Files - 1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := s.limits.Files - 2; i >= 0; i-- {
		if err := os.Rename(s.path(i), s.path(i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return s.open()
}
func (s *jsonSink) Write(p []byte) (int, error) {
	if time.Now().Before(s.retryAfter) {
		return 0, fmt.Errorf("diagnostic disk writes paused")
	}
	record := struct {
		Time    string `json:"time"`
		Message string `json:"message"`
	}{time.Now().UTC().Format(time.RFC3339Nano), strings.TrimRight(string(p), "\r\n")}
	b, err := json.Marshal(record)
	if err != nil {
		return 0, err
	}
	b = append(b, '\n')
	if int64(len(b)) > s.limits.FileBytes {
		return 0, fmt.Errorf("diagnostic record exceeds file limit")
	}
	if s.file == nil {
		err = s.open()
	}
	if err == nil && s.size+int64(len(b)) > s.limits.FileBytes {
		err = s.rotate()
	}
	if err == nil {
		var n int
		n, err = s.file.Write(b)
		s.size += int64(n)
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		s.retryAfter = time.Now().Add(30 * time.Second)
		return 0, err
	}
	if s.console != nil {
		if _, err = s.console.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
