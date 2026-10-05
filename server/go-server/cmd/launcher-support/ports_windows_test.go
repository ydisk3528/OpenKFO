//go:build windows

package main

import (
	"net"
	"os"
	"testing"
)

func TestPortOwnerIdentity(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	rows, e := portOwners(port)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range rows {
		if p.PID == uint32(os.Getpid()) && p.Port == port && p.Protocol == "TCP" {
			if p.Name == "" || p.Image == "" || p.Created == 0 || p.CanStop {
				t.Fatalf("unsafe owner: %+v", p)
			}
			if stopPortOwner(request{Op: "stop_port_owner", PID: p.PID, Port: p.Port, Protocol: p.Protocol, Created: p.Created + 1, Image: p.Image}) == nil {
				t.Fatal("accepted stale identity")
			}
			return
		}
	}
	t.Fatal("listener owner missing")
}
