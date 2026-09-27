//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Optional machine-local settings; the regular CLI and production are unchanged.
func prepareLocalRedis(root string) error {
	path := filepath.Join(root, "redis.local.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var c struct {
		Address      string `json:"address"`
		PasswordFile string `json:"password_file"`
		Namespace    string `json:"namespace"`
		Executable   string `json:"executable"`
		ConfigFile   string `json:"config_file"`
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("invalid redis.local.json")
	}
	host, _, err := net.SplitHostPort(c.Address)
	if err != nil || host != "127.0.0.1" || c.Namespace == "" {
		return fmt.Errorf("local Redis requires 127.0.0.1 and an isolated namespace")
	}
	for key, value := range map[string]string{"OPENKFO_REDIS_ADDR": c.Address, "OPENKFO_REDIS_PASSWORD_FILE": c.PasswordFile, "OPENKFO_REDIS_NAMESPACE": c.Namespace} {
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	probe := func() bool {
		conn, e := net.DialTimeout("tcp", c.Address, 100*time.Millisecond)
		if e != nil {
			return false
		}
		conn.Close()
		return true
	}
	if probe() {
		return nil
	}
	if c.Executable == "" || c.ConfigFile == "" {
		return nil
	} // cache will fall back
	cmd := exec.Command(c.Executable, c.ConfigFile)
	cmd.Dir = filepath.Dir(c.Executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err = cmd.Start(); err != nil {
		log.Print("local Redis could not start; rankings will fall back to MySQL")
		return nil
	}
	go func() { _ = cmd.Wait() }()
	for i := 0; i < 20; i++ {
		if probe() {
			log.Print("local Redis listener ready")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Print("local Redis not ready; rankings will fall back to MySQL")
	return nil
}
