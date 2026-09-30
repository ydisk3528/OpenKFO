package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementConnectionPrecedenceAndSave(t *testing.T) {
	oldHost, oldUser, oldKey := DefaultManagementHost, DefaultManagementUser, DefaultManagementKey
	defer func() { DefaultManagementHost, DefaultManagementUser, DefaultManagementKey = oldHost, oldUser, oldKey }()
	DefaultManagementHost, DefaultManagementUser, DefaultManagementKey = "aaa.vxziouwkf.top", "root", "build-key"
	dir := t.TempDir()
	settings := filepath.Join(dir, "gm-settings.json")
	if err := os.WriteFile(settings, []byte(`{"root":"keep-root","client_directory":"keep-client"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(dir)
	a.GMSettings = settings
	c, err := a.managementConnection()
	if err != nil || c.Host != DefaultManagementHost {
		t.Fatalf("build default: %#v %v", c, err)
	}
	key := filepath.Join(dir, "key.pem")
	os.WriteFile(key, []byte("test"), 0600)
	_, err = a.Call(Request{Operation: "management_connection_save", Connection: &connection{Host: "other.example.com", User: "root", Key: key, Port: 22}})
	if err != nil {
		t.Fatal(err)
	}
	c, err = a.managementConnection()
	if err != nil || c.Host != "other.example.com" {
		t.Fatalf("saved override: %#v %v", c, err)
	}
	data, _ := os.ReadFile(settings)
	if !strings.Contains(string(data), "keep-root") || !strings.Contains(string(data), "keep-client") {
		t.Fatal("unrelated settings lost")
	}
	_, err = a.Call(Request{Operation: "management_connection_save", Connection: &connection{Host: "https://bad/path", User: "root", Key: key, Port: 22}})
	if err == nil {
		t.Fatal("invalid host accepted")
	}
	c, _ = a.managementConnection()
	if c.Host != "other.example.com" {
		t.Fatal("invalid save changed connection")
	}
	os.WriteFile(settings, []byte("broken"), 0600)
	if _, err = a.managementConnection(); err == nil {
		t.Fatal("broken override fell back silently")
	}
}
