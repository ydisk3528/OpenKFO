package bridge

import (
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type nativeDelivery struct {
	data   []byte
	readAt time.Time
	trace  *packetTrace
	close  bool
}

// One ordered writer per native socket. A stalled game must not stop TLS reads.
type nativeChannel struct {
	net.Conn
	session *remoteSession
	id      uint32
	output  chan nativeDelivery
	stopped chan struct{}
	once    sync.Once
	bytes   atomic.Int64
}

func newNativeChannel(s *remoteSession, id uint32, c net.Conn) *nativeChannel {
	return &nativeChannel{Conn: c, session: s, id: id, output: make(chan nativeDelivery, 128), stopped: make(chan struct{})}
}

func (c *nativeChannel) Close() error {
	var err error
	c.once.Do(func() { close(c.stopped); err = c.Conn.Close() })
	return err
}

func (c *nativeChannel) enqueue(d nativeDelivery) bool {
	select {
	case <-c.stopped:
		return true
	default:
	}
	n := int64(len(d.data) + 128)
	if c.bytes.Add(n) > 2*1024*1024 {
		c.bytes.Add(-n)
		return false
	}
	select {
	case c.output <- d:
		return true
	default:
		c.bytes.Add(-n)
		return false
	}
}

func (c *nativeChannel) writeLoop() {
	defer c.Close()
	var lastSlow time.Time
	for {
		select {
		case <-c.session.done:
			return
		case <-c.stopped:
			return
		case d := <-c.output:
			if d.close {
				c.bytes.Add(-int64(len(d.data) + 128))
				return
			}
			packets := d.trace.decode(d.data)
			d.trace.record("server_read", d.readAt, packets, nil)
			started := time.Now()
			c.SetWriteDeadline(started.Add(5 * time.Second))
			n, err := c.Write(d.data)
			c.bytes.Add(-int64(len(d.data) + 128))
			if err == nil && n != len(d.data) {
				err = io.ErrShortWrite
			}
			finished := time.Now()
			event := "native_write_complete"
			if err != nil {
				event = "native_write_failed"
			}
			d.trace.record(event, finished, packets, err)
			if finished.Sub(d.readAt) > 100*time.Millisecond && finished.Sub(lastSlow) > 5*time.Second {
				lastSlow = finished
				log.Printf("native_send_slow uid=%d channel=%d queue_ms=%d write_ms=%d remaining=%d", c.session.uid, c.id, started.Sub(d.readAt).Milliseconds(), finished.Sub(started).Milliseconds(), len(c.output))
			}
			if err != nil {
				select {
				case <-c.stopped:
					return
				default:
				}
				log.Printf("native_write_failed uid=%d channel=%d bytes=%d error=%v", c.session.uid, c.id, n, err)
				c.session.close()
				return
			}
		}
	}
}
