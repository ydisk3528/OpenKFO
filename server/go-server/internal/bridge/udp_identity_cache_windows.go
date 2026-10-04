package bridge

import (
	"net"
	"syscall"
	"time"
)

type udpIdentityEntry struct {
	identity Identity
	handle   syscall.Handle
	expires  time.Time
}

// Owned solely by Bridge.datagrams. Short leases bound stale port ownership;
// retained process handles detect exit/PID reuse even before lease expiry.
type udpIdentityCache struct{ entries map[int]udpIdentityEntry }

func newUDPIdentityCache() *udpIdentityCache {
	return &udpIdentityCache{entries: make(map[int]udpIdentityEntry)}
}
func (c *udpIdentityCache) close() {
	for port, e := range c.entries {
		syscall.CloseHandle(e.handle)
		delete(c.entries, port)
	}
}
func (c *udpIdentityCache) resolve(peer *net.UDPAddr, image string) (Identity, error) {
	if !peer.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return Identity{}, errIdentity
	}
	now := time.Now()
	for port, e := range c.entries {
		state, err := syscall.WaitForSingleObject(e.handle, 0)
		if err != nil || state != uint32(syscall.WAIT_TIMEOUT) || !now.Before(e.expires) || e.identity.Image != image {
			syscall.CloseHandle(e.handle)
			delete(c.entries, port)
		}
	}
	if e, ok := c.entries[peer.Port]; ok {
		return e.identity, nil
	}
	id, err := udpIdentity(peer, image)
	if err != nil {
		return Identity{}, err
	}
	h, err := syscall.OpenProcess(0x100000|0x1000, false, id.PID)
	if err != nil {
		return Identity{}, err
	}
	// Verify the retained handle belongs to the same lifetime as the lookup.
	var created, exited, kernelTime, userTime syscall.Filetime
	err = syscall.GetProcessTimes(h, &created, &exited, &kernelTime, &userTime)
	if err != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != id.Created {
		syscall.CloseHandle(h)
		return Identity{}, errIdentity
	}
	if len(c.entries) >= 64 {
		c.close()
	}
	c.entries[peer.Port] = udpIdentityEntry{id, h, now.Add(250 * time.Millisecond)}
	return id, nil
}
