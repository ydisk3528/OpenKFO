//go:build windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalRedisOptionalAndLoopbackOnly(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"OPENKFO_REDIS_ADDR", "OPENKFO_REDIS_PASSWORD_FILE", "OPENKFO_REDIS_NAMESPACE"} {
		t.Setenv(key, "")
	}
	if err := prepareLocalRedis(root); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENKFO_REDIS_ADDR") != "" {
		t.Fatal("absent configuration changed defaults")
	}
	path := filepath.Join(root, "redis.local.json")
	data := []byte(`{"address":"example.com:6380","namespace":"offline"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareLocalRedis(root); err == nil {
		t.Fatal("remote Redis accepted in local auto-start")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	data, _ = json.Marshal(map[string]string{"address": listener.Addr().String(), "namespace": "offline-test", "executable": "must-not-start"})
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = prepareLocalRedis(root); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENKFO_REDIS_ADDR") != listener.Addr().String() || os.Getenv("OPENKFO_REDIS_NAMESPACE") != "offline-test" {
		t.Fatal("cache environment missing")
	}
}
