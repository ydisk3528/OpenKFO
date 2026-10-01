package game

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/releases"
	"kungfu.local/server/internal/tunnel"
)

type loginLimit struct {
	Attempts int
	Since    time.Time
}
type Server struct {
	loginTextMu sync.Mutex
	loginTextAt time.Time
	loginText   atomic.Pointer[map[string]string]

	udp         *net.UDPConn
	udpMutex    sync.Mutex
	udpPeers    map[[16]byte]*serverDatagramPeer
	Hub         *Hub
	Certificate tls.Certificate
	connections chan struct{}
	hashing     chan struct{}
	limitMutex  sync.Mutex
	attempts    map[string]loginLimit
}

func NewServer(hub *Hub, certificate tls.Certificate) *Server {
	// Bind transport receipts to the origin identity, not a process lifetime.
	// Password authentication is still required before a receipt is consumed.
	if key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey); err == nil {
		digest := sha256.Sum256(append([]byte("openkfo/peer-receipt/v1\x00"), key...))
		hub.PeerKey = digest[:]
	}
	// Active game connections must not exhaust admission before MaxSessions.
	// Leave space for launcher probes and reconnecting clients as well.
	return &Server{Hub: hub, Certificate: certificate, connections: make(chan struct{}, hub.Config.sessionLimit()+128), hashing: make(chan struct{}, 2), attempts: map[string]loginLimit{}}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if server.Hub.Config.ExperimentalNeutralNPC && server.Hub.Config.LocalNeutralNPCProbe {
		mux.HandleFunc("GET /experimental/neutral-npc", server.Hub.neutralNPCStatus)
		mux.HandleFunc("GET /experimental/neutral-npc-plan", server.Hub.neutralNPCStatus)
		mux.HandleFunc("POST /experimental/neutral-npc-failure", server.Hub.neutralNPCFailure)
	}
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := server.Hub.Store.DB.PingContext(ctx); err != nil {
			http.Error(writer, "unavailable", 503)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(struct {
			Service string `json:"service"`
			Status  string `json:"status"`
			Key     string `json:"launcher_credentials_key,omitempty"`
		}{"kungfu-go", "ok", server.Hub.Config.LauncherCredentialsKey})
	})
	mux.HandleFunc("GET /kk/tunnel", server.accept)
	mux.Handle("GET /updates/", releases.Handler(os.Getenv("OPENKFO_UPDATES_DIR")))
	return mux
}

func (server *Server) allowLogin(account string) bool {
	server.limitMutex.Lock()
	defer server.limitMutex.Unlock()
	now := time.Now()
	for name, limit := range server.attempts {
		if now.Sub(limit.Since) > time.Minute {
			delete(server.attempts, name)
		}
	}
	account = strings.ToLower(account)
	limit := server.attempts[account]
	if limit.Attempts >= 5 || len(server.attempts) >= 4096 {
		return false
	}
	if limit.Since.IsZero() {
		limit.Since = now
	}
	limit.Attempts++
	server.attempts[account] = limit
	return true
}

func (server *Server) accept(writer http.ResponseWriter, request *http.Request) {
	select {
	case server.connections <- struct{}{}:
		defer func() { <-server.connections }()
	default:
		http.Error(writer, "busy", 503)
		return
	}
	// This endpoint is for the packaged bridge; do not admit web page origins.
	upgrader := websocket.Upgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(request *http.Request) bool { return request.Header.Get("Origin") == "" }}
	socket, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer socket.Close()
	socket.SetReadLimit(2 * 1024 * 1024)
	connection := tls.Server(&tunnel.Conn{WS: socket}, &tls.Config{Certificates: []tls.Certificate{server.Certificate}, MinVersion: tls.VersionTLS12})
	server.serveConnection(connection)
}

// ServeTLS carries the same authenticated protocol directly, without HTTP.
func (server *Server) ServeTLS(listener net.Listener) error {
	for {
		raw, err := listener.Accept()
		if err != nil {
			return err
		}
		select {
		case server.connections <- struct{}{}:
			go func() {
				defer func() { <-server.connections }()
				defer raw.Close()
				connection := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{server.Certificate}, MinVersion: tls.VersionTLS12})
				server.serveConnection(connection)
			}()
		default:
			raw.Close()
		}
	}
}

func (server *Server) serveConnection(connection *tls.Conn) {
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(20 * time.Second))
	if err := connection.Handshake(); err != nil {
		return
	}
	reader := bufio.NewReaderSize(connection, 65536)
	authBytes, err := tunnel.ReadFrame(reader, 8192)
	if err != nil {
		return
	}
	var auth tunnel.Frame
	if json.Unmarshal(authBytes, &auth) != nil {
		return
	}
	probe := &Session{Trace: server.Hub.Trace, Account: auth.Account}
	probe.tracePacket("C->S", 0, "tunnel:"+auth.Op, 0, nil, true)
	if auth.Op == "health" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		response := tunnel.Frame{Op: "health", Value: 1, LauncherCredentialsKey: server.Hub.Config.LauncherCredentialsKey}
		if server.Hub.Store.DB.PingContext(ctx) != nil {
			response.Value = 0
			response.Error = "unavailable"
		}
		probe.traceFrame("S->C", response)
		json.NewEncoder(connection).Encode(response)
		return
	}
	if auth.Op != "auth" || len(auth.Account) > 20 || len(auth.Password) != 64 || len(auth.PeerReceipt) > 72 {
		return
	}
	encoder := json.NewEncoder(connection)
	deny := func(reason string) {
		log.Printf("login_rejected account=%q reason=%s", auth.Account, reason)
		response := tunnel.Frame{Op: "auth", Error: reason, ErrorMessage: server.loginErrorText(reason)}
		probe.traceFrame("S->C", response)
		encoder.Encode(response)
	}
	if auth.ConfigHash != server.Hub.Config.ConfigHash {
		log.Printf("client_config_mismatch account=%q client_hash=%.64q server_hash=%.64q action=allow", auth.Account, auth.ConfigHash, server.Hub.Config.ConfigHash)
	}
	if !server.allowLogin(auth.Account) {
		deny("rate_limited")
		return
	}
	select {
	case server.hashing <- struct{}{}:
	default:
		deny("busy")
		return
	}
	account, err := server.Hub.Store.AuthenticateOrRegister(auth.Account, auth.Password)
	<-server.hashing
	auth.Password = ""
	if err != nil {
		if errors.Is(err, persistence.ErrAccountBanned) {
			deny("account_banned")
		} else if errors.Is(err, persistence.ErrDenied) {
			deny("invalid_credentials")
		} else {
			log.Printf("login_database_failed account=%q error_type=%T", auth.Account, err)
			deny("server_error")
		}
		return
	}
	if err := server.Hub.checkLoginRelease(context.Background()); err != nil {
		deny("server_config_unavailable")
		return
	}
	stageAccess, stageErr := server.Hub.Store.StageAccess()
	if stageErr != nil {
		deny("server_config_unavailable")
		return
	}
	if stageErr = server.Hub.Config.ValidateStageAccess(stageAccess); stageErr != nil {
		log.Printf("login_server_config_mismatch: %v", stageErr)
		deny("server_config_mismatch")
		return
	}
	session, err := server.Hub.Attach(account, auth.Port, auth.PeerReceipt)
	if err != nil {
		if errors.Is(err, errPeerOccupied) {
			deny("peer_receipt_occupied_restart_game")
		} else if errors.Is(err, errPeerReceipt) {
			deny("peer_receipt_invalid_restart_game")
		} else if errors.Is(err, errAccountOnline) {
			deny("account_already_online")
		} else if errors.Is(err, errServerFull) {
			deny("server_full")
		} else if errors.Is(err, errClientPort) {
			deny("invalid_client_port")
		} else {
			deny("server_error")
		}
		return
	}
	session.ClientRelease = auth.ClientRelease
	session.ClientConfigHash = auth.ConfigHash
	defer server.Hub.Detach(session)
	grant, peer := server.registerDatagramPeer(session)
	defer server.unregisterDatagramPeer(peer)
	if peer != nil {
		session.datagramEnabled.Store(true)
		go session.writeDatagrams(peer.send)
	}
	if err = encoder.Encode(tunnel.Frame{Op: "auth", UID: session.UID, UDP: grant}); err != nil {
		return
	}
	session.tracePacket("S->C", 0, "tunnel:auth-ok", 0, nil, false)
	log.Printf("authenticated uid=%d account=%q player=%q", session.UID, session.Account, session.Nickname)
	connection.SetDeadline(time.Time{})
	go func() {
		ticker := time.NewTicker(time.Second)
		inventoryTicker := time.NewTicker(15 * time.Second)
		defer inventoryTicker.Stop()
		defer ticker.Stop()
		for {
			select {
			case <-session.Done:
				return
			case <-ticker.C:
				ban, err := server.Hub.Store.AccountBan(session.UID)
				if err != nil {
					log.Printf("account_ban_check_failed uid=%d", session.UID)
					continue
				}
				if ban.Active(time.Now().Unix()) || ban.Generation != account.BanGeneration {
					log.Printf("account_banned_disconnect uid=%d account=%q generation=%d", session.UID, session.Account, ban.Generation)
					session.Close()
					return
				}
			case <-inventoryTicker.C:
				if err := server.Hub.RefreshExpiredInventory(session); err != nil {
					log.Printf("inventory_refresh_failed uid=%d", session.UID)
				}
			}
		}
	}()
	go func() {
		defer session.Close()
		defer connection.Close()
		var lastSlowWrite time.Time
		for {
			select {
			case <-session.Done:
				return
			case frame := <-session.Output:
				session.queuedBytes.Add(-int64(len(frame.Data) + 128))
				started := time.Now()
				queued := time.Duration(0)
				if !frame.QueuedAt.IsZero() {
					queued = started.Sub(frame.QueuedAt)
				}
				report := func() {
					finished := time.Now()
					if (queued > 100*time.Millisecond || finished.Sub(started) > 100*time.Millisecond) && finished.Sub(lastSlowWrite) > 5*time.Second {
						lastSlowWrite = finished
						log.Printf("game_send_slow uid=%d op=%s queue_ms=%d write_ms=%d remaining=%d", session.UID, frame.Op, queued.Milliseconds(), finished.Sub(started).Milliseconds(), len(session.Output))
					}
				}
				if peer != nil && frame.Op == "udp" && frame.PeerReceipt == "" && peer.send(frame) {
					report()
					continue
				}
				connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
				writeErr := encoder.Encode(frame)
				report()
				if writeErr != nil {
					log.Printf("session_write_end uid=%d op=%s error=%v", session.UID, frame.Op, writeErr)
					return
				}
			}
		}
	}()
	for {
		connection.SetReadDeadline(time.Now().Add(75 * time.Second))
		if session.LoggedOut {
			connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		}
		encoded, err := tunnel.ReadFrame(reader, 100000)
		if err != nil {
			log.Printf("session_read_end uid=%d account=%q error=%v", session.UID, session.Account, err)
			break
		}
		var frame tunnel.Frame
		if json.Unmarshal(encoded, &frame) != nil {
			log.Printf("session_bad_frame uid=%d account=%q bytes=%d", session.UID, session.Account, len(encoded))
			break
		}
		if err = server.Hub.Handle(session, frame); err != nil {
			log.Printf("session_rejected uid=%d account=%q op=%q channel=%d kind=%q error=%v", session.UID, session.Account, frame.Op, frame.Channel, frame.Kind, err)
			break
		}
	}
	log.Printf("disconnected uid=%d", session.UID)
}
