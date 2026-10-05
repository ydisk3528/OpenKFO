package game

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestAuthenticatedWebSocketTLSHandoff(t *testing.T) {
	t.Run("websocket", func(t *testing.T) { testAuthenticatedTransport(t, false, false, true) })
	t.Run("direct_tls", func(t *testing.T) { testAuthenticatedTransport(t, true, false, true) })
	t.Run("ordered_direct_tls", func(t *testing.T) { testAuthenticatedTransport(t, true, true, true) })
	t.Run("ordered_websocket", func(t *testing.T) { testAuthenticatedTransport(t, false, true, true) })
	t.Run("ordered_requested_without_udp_listener", func(t *testing.T) { testAuthenticatedTransport(t, true, true, false) })
}

func testAuthenticatedTransport(t *testing.T, direct, ordered, udpEnabled bool) {
	dsn := os.Getenv("KK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	store, err := persistence.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	var databaseName string
	if store.DB.QueryRow("SELECT DATABASE()").Scan(&databaseName) != nil || databaseName != "kungfu_game_test" {
		t.Fatal("isolated test database required")
	}
	uid := uint64(time.Now().UnixNano() / 1000)
	account, err := persistence.NewAccountWithStarterCharacter(uid, "ws"+hex.EncodeToString(protocol.Uint64Bytes(uid))[:14], "transport123")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Create(account); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificate, err := tunnel.Certificate(directory)
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(store, Config{ConfigHash: strings.Repeat("a", 64)})
	server := NewServer(hub, certificate)
	if udpEnabled {
		stopUDP, err := server.ListenDatagrams("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer stopUDP()
	}
	endpoint := httptest.NewTLSServer(server.Handler())
	defer endpoint.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.ServeTLS(listener)
	outerRoots := x509.NewCertPool()
	outerRoots.AddCert(endpoint.Certificate())
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: outerRoots}}
	connect := func() *tls.Conn {
		t.Helper()
		var raw net.Conn
		if direct {
			raw, err = net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
		} else {
			var socket *websocket.Conn
			socket, _, err = dialer.Dial("wss"+strings.TrimPrefix(endpoint.URL, "https")+"/kk/tunnel", nil)
			if err == nil {
				raw = &tunnel.Conn{WS: socket}
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		pem, err := os.ReadFile(filepath.Join(directory, "origin.crt"))
		if err != nil {
			t.Fatal(err)
		}
		roots.AppendCertsFromPEM(pem)
		connection := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: "kk-origin", MinVersion: tls.VersionTLS12})
		connection.SetDeadline(time.Now().Add(10 * time.Second))
		if err = connection.Handshake(); err != nil {
			t.Fatal(err)
		}
		return connection
	}
	health := connect()
	if err = json.NewEncoder(health).Encode(tunnel.Frame{Op: "health"}); err != nil {
		t.Fatal(err)
	}
	healthBytes, err := tunnel.ReadFrame(bufio.NewReader(health), 8192)
	var healthReply tunnel.Frame
	if err != nil || json.Unmarshal(healthBytes, &healthReply) != nil || healthReply.Op != "health" || healthReply.Value != 1 {
		t.Fatal("health probe failed")
	}
	health.Close()
	connection := connect()
	defer connection.Close()
	reader := bufio.NewReader(connection)
	encoder := json.NewEncoder(connection)
	send := func(frame tunnel.Frame) {
		t.Helper()
		if err := encoder.Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() tunnel.Frame {
		t.Helper()
		encoded, err := tunnel.ReadFrame(reader, 2*1024*1024)
		if err != nil {
			t.Fatal(err)
		}
		var frame tunnel.Frame
		if err = json.Unmarshal(encoded, &frame); err != nil {
			t.Fatal(err)
		}
		return frame
	}
	passwordHash := sha256.Sum256([]byte("xfmRn9z7K1wTfvBYhpCwZmE8yLWN1oLvtransport123"))
	send(tunnel.Frame{Op: "auth", UDPOrder: ordered, Kind: "udp-drain-v1", Account: account.Account, Password: hex.EncodeToString(passwordHash[:]), ConfigHash: hub.Config.ConfigHash, Port: 18001})
	if reply := receive(); reply.UID != uid || reply.Error != "" || (reply.UDP != nil) != udpEnabled || (reply.UDP != nil && reply.UDP.Order != ordered) {
		t.Fatal("authentication rejected")
	}
	// Native bootstrap must work over TCP even without a UDP listener/path.
	probe := make([]byte, 24+141)
	protocol.WriteUint16(probe, 0, 1)
	protocol.WriteUint16(probe, 2, 1001)
	var epoch uint32
	if ordered && udpEnabled {
		epoch = 1
	}
	send(tunnel.Frame{Op: "udp", Port: 18001, Data: probe, Value: epoch})
	if reply := receive(); reply.Op != "udp" || len(reply.Data) < 24 || protocol.ReadUint16(reply.Data, 2) != 1002 {
		t.Fatal("native TCP fallback bootstrap failed")
	}
	send(tunnel.Frame{Op: "ready"})
	send(tunnel.Frame{Op: "open", Channel: 1, Kind: "sdk"})
	login := protocol.LoginEncode(protocol.Message{ID: 1001})
	login[6] = 1
	send(tunnel.Frame{Op: "data", Channel: 1, Data: login})
	if reply := receive(); reply.Channel != 1 || len(reply.Data) < 10 || protocol.ReadUint16(reply.Data, 8) != 1002 {
		t.Fatal("SDK acknowledgement invalid")
	}
	send(tunnel.Frame{Op: "open", Channel: 2, Kind: "game"})
	hello := make([]byte, 96)
	protocol.WriteUint64(hello, 0, uid)
	protocol.WriteUint32(hello, 49, 594)
	sendGame := func(channel, id uint32, payload []byte) {
		t.Helper()
		encoded, _ := protocol.Encode(protocol.Message{ID: id, Payload: payload})
		send(tunnel.Frame{Op: "data", Channel: channel, Data: encoded})
	}
	readGame := func(channel uint32) protocol.Message {
		t.Helper()
		frame := receive()
		if frame.Channel != channel {
			t.Fatal("wrong channel")
		}
		decoder := protocol.Decoder{}
		messages, err := decoder.Feed(frame.Data)
		if err != nil || len(messages) != 1 {
			t.Fatal("bad game response")
		}
		return messages[0]
	}
	sendGame(2, 1010, hello)
	for _, expected := range []uint32{1131, 1020, 1230, 1120, 1035, 7080, 7070, 1038, 1151, protocol.MsgPlayerPreferences} {
		if reply := readGame(2); reply.ID != expected {
			t.Fatalf("expected %d got %d", expected, reply.ID)
		}
	}
	sendGame(2, 3320, protocol.Uint32Bytes(1))
	if readGame(2).ID != 3330 || readGame(2).ID != 1201 {
		t.Fatal("handoff grant missing")
	}
	send(tunnel.Frame{Op: "open", Channel: 3, Kind: "game"})
	sendGame(3, 2010, hello)
	if readGame(3).ID != 2030 {
		t.Fatal("lobby context missing")
	}
	send(tunnel.Frame{Op: "ping"})
	if receive().Op != "pong" {
		t.Fatal("keepalive failed")
	}
}
