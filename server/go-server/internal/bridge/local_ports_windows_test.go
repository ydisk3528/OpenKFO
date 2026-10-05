package bridge

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoPortsHoldSocketsAndWriteGameConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Data"), 0700); err != nil {
		t.Fatal(err)
	}
	xml := []byte("<?xml version=\"1.0\" encoding=\"gb2312\"?><GameClient><LoginServer Name=\"\xd2\xbb\" Ip=\"127.0.0.1\" Port=\"18000\" /></GameClient>")
	path := filepath.Join(root, "Data", "config.xml")
	if err := os.WriteFile(path, xml, 0600); err != nil {
		t.Fatal(err)
	}
	ls, u, c, err := bindLocalPorts(Config{AutoPorts: true, ClientDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	defer func() {
		for _, l := range ls {
			l.Close()
		}
	}()
	seen := map[int]bool{}
	for _, port := range []int{c.LoginPort, c.SDKPort, c.GamePort} {
		if port <= 0 || seen[port] {
			t.Fatal("invalid allocated ports", c)
		}
		seen[port] = true
	}
	if u.LocalAddr().(*net.UDPAddr).Port != c.GamePort {
		t.Fatal("TCP/UDP game port differs")
	}
	_, _, _, err = bindLocalPorts(cWithManual(c))
	if err == nil {
		t.Fatal("reserved sockets not held")
	}
	if err = writeLocalPorts(c); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "\xd2\xbb") || strings.Contains(string(got), "Port=\"18000\"") {
		t.Fatal("SDK config not patched or encoding changed")
	}
	ini, _ := os.ReadFile(filepath.Join(root, "server.ini"))
	if !strings.Contains(string(ini), "ip=127.0.0.1") {
		t.Fatal("login config missing")
	}
}

func cWithManual(c Config) Config { c.AutoPorts = false; return c }

func TestGameUDPConflictReleasesTCPReservations(t *testing.T) {
	ls, u, c, err := bindLocalPorts(Config{AutoPorts: true})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for _, l := range ls {
		l.Close()
	}
	if _, _, _, err = bindLocalPorts(cWithManual(c)); err == nil {
		t.Fatal("UDP conflict ignored")
	}
	for _, port := range []int{c.LoginPort, c.SDKPort, c.GamePort} {
		l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		if err != nil {
			t.Fatal("TCP leaked after UDP failure", err)
		}
		l.Close()
	}
}
