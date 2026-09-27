package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The local window starts a no-argument worker. Explicit CLI arguments retain
// the normal server/admin behavior, including externally supplied database DSNs.
// settings.private.json selects the database path: a filled ssh_config builds
// the SSH tunnel to the remote independent test database, an empty one connects
// to the loopback MySQL named by the DSN directly.
func prepareLocalConsole() (func(), error) {
	noop := func() {}
	if len(os.Args) > 1 {
		return noop, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return noop, err
	}
	root := filepath.Dir(executable)
	var settings struct {
		DSN       string `json:"dsn"`
		SSHConfig string `json:"ssh_config"`
		Database  string `json:"database"`
	}
	readJSON := func(path string, target any) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), target)
	}
	if err = readJSON(filepath.Join(root, "settings.private.json"), &settings); err != nil {
		return noop, err
	}
	if err = prepareLocalRedis(root); err != nil {
		return noop, err
	}
	db, err := mysql.ParseDSN(settings.DSN)
	if err != nil {
		return noop, fmt.Errorf("invalid debug database configuration")
	}
	host, _, err := net.SplitHostPort(db.Addr)
	if err != nil || db.Net != "tcp" || host != "127.0.0.1" || db.DBName == "" || db.DBName != settings.Database {
		return noop, fmt.Errorf("debug DSN must use a loopback TCP address and the configured independent database")
	}
	cleanup := noop
	if settings.SSHConfig != "" {
		// SSH tunnel mode: settings.private.json points at a credential file
		// forwarding the local DSN port to the remote host's own 127.0.0.1:3306.
		sshPath := settings.SSHConfig
		if !filepath.IsAbs(sshPath) {
			sshPath = filepath.Join(root, sshPath)
		}
		var remote struct {
			Host, User, Key string
			Port            int
		}
		if err = readJSON(sshPath, &remote); err != nil {
			return noop, err
		}
		if !filepath.IsAbs(remote.Key) {
			remote.Key = filepath.Join(filepath.Dir(sshPath), remote.Key)
		}
		key, err := os.ReadFile(remote.Key)
		if err != nil {
			return noop, err
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return noop, fmt.Errorf("cannot load SSH private key: %w", err)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return noop, err
		}
		verifyHost, err := knownhosts.New(filepath.Join(home, ".ssh", "known_hosts"))
		if err != nil {
			return noop, err
		}
		listener, err := net.Listen("tcp", db.Addr)
		if err != nil {
			return noop, fmt.Errorf("database tunnel port is in use: %w", err)
		}
		address := net.JoinHostPort(remote.Host, strconv.Itoa(remote.Port))
		fmt.Println("LOCAL DEBUG SERVER | connecting independent database:", settings.Database)
		raw, err := net.DialTimeout("tcp", address, 10*time.Second)
		if err != nil {
			listener.Close()
			return noop, err
		}
		raw.SetDeadline(time.Now().Add(15 * time.Second))
		connection, channels, requests, err := ssh.NewClientConn(raw, address, &ssh.ClientConfig{
			User: remote.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: verifyHost,
		})
		if err != nil {
			raw.Close()
			listener.Close()
			return noop, err
		}
		raw.SetDeadline(time.Time{})
		client := ssh.NewClient(connection, channels, requests)
		cleanup = func() { listener.Close(); client.Close() }
		go func() {
			for {
				local, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					defer local.Close()
					remote, err := client.Dial("tcp", "127.0.0.1:3306")
					if err != nil {
						fmt.Fprintln(os.Stderr, "Database tunnel connection failed:", err)
						return
					}
					defer remote.Close()
					go func() { io.Copy(remote, local); remote.Close() }()
					io.Copy(local, remote)
				}()
			}
		}()
	} else {
		// Direct mode: the DSN already names a loopback MySQL account, so no
		// tunnel is needed. Empty ssh_config is the documented selector here;
		// the earlier unconditional Join(root, "") read the directory itself
		// and failed with a misleading "Incorrect function" error.
		fmt.Println("LOCAL DEBUG SERVER | direct database connection:", settings.Database)
	}
	logs := filepath.Join(root, "logs")
	if err = os.MkdirAll(logs, 0700); err != nil {
		cleanup()
		return noop, err
	}
	logPath := filepath.Join(logs, "protocol-"+time.Now().Format("20060102-150405")+".log")
	if err = os.Setenv("KK_MYSQL_DSN", settings.DSN); err != nil {
		cleanup()
		return noop, err
	}
	os.Args = append(os.Args, "-config", filepath.Join(root, "config.json"), "-cert-dir", filepath.Join(root, "certificates"),
		"-listen", "127.0.0.1:19090", "-tls-listen", "127.0.0.1:19091", "-trace-protocol", "-protocol-log", logPath)
	fmt.Println("TLS: 127.0.0.1:19091 | Protocol log:", logPath)
	fmt.Println("Local server ready; use the local monitor window to stop the server.")
	return cleanup, nil
}
