package tunnel

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestQueueTimingDoesNotChangeWireFrame(t *testing.T) {
	frame := Frame{Op: "data", Channel: 3, Data: []byte{1, 2, 3}}
	before, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	frame.QueuedAt = time.Now()
	after, err := json.Marshal(frame)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("timing leaked into protocol: %s %v", after, err)
	}
}
