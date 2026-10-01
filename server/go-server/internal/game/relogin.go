package game

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
	"time"
)

var errPeerReceipt = errors.New("invalid transport receipt")
var errPeerOccupied = errors.New("transport receipt belongs to an active connection")

// A transport-only capability, never an account login token. Bound to the
// origin certificate; the bridge retains it only for the same native process.
func (hub *Hub) peerReceipt(id uint32) string {
	mac := hmac.New(sha256.New, hub.PeerKey)
	body := protocol.Uint32Bytes(id)
	mac.Write(body)
	return hex.EncodeToString(append(body, mac.Sum(nil)...))
}

// Caller holds Hub.Mutex and has already authenticated the new account.
func (hub *Hub) resumePeer(s *Session, receipt string) error {
	if receipt == "" {
		return nil
	}
	data, err := hex.DecodeString(receipt)
	if err != nil || len(data) != 36 {
		return errPeerReceipt
	}
	id := protocol.ReadUint32(data, 0)
	if id == 0 || !hmac.Equal([]byte(receipt), []byte(hub.peerReceipt(id))) {
		return errPeerReceipt
	}
	for _, other := range hub.Sessions {
		if other != s && !other.LoggedOut && other.P2P == id {
			select {
			case <-other.Done:
				// Close can precede deferred Detach. Release only a confirmed
				// closed session; never take an active process's transport ID.
				hub.leave(other, false)
				hub.removeHornSession(other)
				delete(hub.Sessions, other.UID)
			default:
				return errPeerOccupied
			}
		}
	}
	s.P2P, s.P2PUntil = id, time.Now().Add(time.Minute)
	if hub.NextPlayer <= id {
		hub.NextPlayer = id + 1
	}
	return nil
}

func (hub *Hub) initPeerKey() error {
	if len(hub.PeerKey) != 0 {
		return nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	hub.PeerKey = key
	return nil
}

// Called only after SDK authentication on the UID-bound encrypted tunnel.
// Preserve that SDK channel and table readiness; native game connections reopen.
func (hub *Hub) resetNativeSession(s *Session, sdk uint32) {
	hub.leave(s, false)
	for id, ch := range s.Channels {
		if id != sdk {
			s.emit(tunnel.Frame{Op: "close", Channel: id})
			ch.LoginBuffer = nil
			// Keep a tombstone until the bridge acknowledges close: bytes already
			// in flight on the old TCP channel must not terminate the new login.
			ch.Phase = "closed"
		}
	}
	hub.removeHornSession(s)
	s.GameChannel, s.BootstrapChannel = 0, 0
	s.LobbyID = 0
	s.TitleOffer = 0
	s.StageViewRequested = false
	s.StageViewReady = false
	s.StageViewDigest = [32]byte{}
	s.ExtendedTaskNotified = nil
	s.WeaponRevision = 0
	s.TalismanQuote = nil
	s.RenewalQuote = nil
	s.MailPreview, s.MailAttachment, s.MailClaimFailed = 0, 0, false
	s.Bound = false
	s.UDPRelayed = 0
	s.HandoffUntil = time.Time{}
	s.ConsumeIntents, s.Inventory = nil, nil
	s.TalismanPending = nil
}
