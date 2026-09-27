package game

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"kungfu.local/server/internal/protocol"
)

// A fully decoded gameplay packet failed validation. Dropping this packet is
// sufficient; authentication, transport and storage errors remain fatal.
type securityRejection struct{ reason string }

func (e *securityRejection) Error() string { return e.reason }
func rejectBattle(format string, args ...any) error {
	return &securityRejection{fmt.Sprintf(format, args...)}
}

const securityLogLimit = 4 * 1024 * 1024

func securityLogName(account string, uid uint64) string {
	if account == "" {
		return fmt.Sprintf("uid-%d_offline.log", uid)
	}
	for _, r := range account {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return fmt.Sprintf("uid-%d_offline.log", uid)
		}
	}
	if len(account) > 100 {
		return fmt.Sprintf("uid-%d_offline.log", uid)
	}
	return account + "_offline.log"
}

// Called under Hub.Mutex. Only gameplay bytes are recorded, never SDK passwords.
func (hub *Hub) recordSecurityRejection(session *Session, channel *Channel, message protocol.Message, cause error) {
	hub.recordSecurityAudit(session, channel, message, cause, false)
}

func (hub *Hub) recordSecurityAudit(session *Session, channel *Channel, message protocol.Message, cause error, forwarded bool) {
	action := "安全验证未通过：仅丢弃消息，保留连接"
	if forwarded {
		action = "BUFF归属校验告警：已放行转发，保留连接"
	}

	directory := hub.SecurityLogDirectory
	if directory == "" {
		directory = filepath.Join("logs", "security")
	}
	var roomID uint16
	var serial uint32
	if session.Room != nil {
		roomID, serial = session.Room.ID, session.Room.Serial
	}
	subID := uint32(0)
	if len(message.Payload) >= 4 {
		subID = protocol.ReadUint32(message.Payload, 0)
	}
	payload := message.Payload
	if len(payload) > 512 {
		payload = payload[:512]
	}
	record := struct {
		Time    string `json:"time"`
		Account string `json:"account"`
		UID     uint64 `json:"uid"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
		Channel uint32 `json:"channel"`
		Message uint32 `json:"message"`
		Event   uint32 `json:"event"`
		Room    uint16 `json:"room"`
		Serial  uint32 `json:"battle_serial"`
		Bytes   int    `json:"bytes"`
		Payload string `json:"payload_hex_prefix"`
	}{time.Now().Format(time.RFC3339Nano), session.Account, session.UID,
		action, cause.Error(), channel.ID, message.ID, subID,
		roomID, serial, len(message.Payload), hex.EncodeToString(payload)}
	// Capture only immutable values; the worker must never read live room/session state.
	uid, account, messageID := session.UID, session.Account, message.ID
	reason := strings.TrimSpace(cause.Error())
	path := filepath.Join(directory, securityLogName(account, uid))
	write := func() {
		hub.auditWriteMutex.Lock()
		defer hub.auditWriteMutex.Unlock()
		// A broken directory produces one warning per minute, not per packet.
		if time.Now().Before(hub.securityAuditRetryAfter) {
			hub.securityAuditSkipped++
			return
		}
		if hub.securityAuditSkipped > 0 {
			log.Printf("security_audit_skipped directory_unavailable=true count=%d", hub.securityAuditSkipped)
			hub.securityAuditSkipped = 0
		}
		data, err := json.Marshal(record)
		if err == nil {
			err = os.MkdirAll(directory, 0700)
		}
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.Size() >= securityLogLimit {
				// Keep one bounded previous file. Account names cannot escape directory.
				if removeErr := os.Remove(path + ".1"); removeErr != nil && !os.IsNotExist(removeErr) {
					err = removeErr
				}
				if err == nil {
					err = os.Rename(path, path+".1")
				}
			}
		}
		if err == nil {
			var file *os.File
			file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err == nil {
				_, err = file.Write(append(data, '\n'))
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
		}
		log.Printf("security_audit connection_kept=true forwarded=%t uid=%d account=%q message=%d event=%d reason=%q", forwarded, uid, account, messageID, subID, reason)
		if err != nil {
			hub.securityAuditRetryAfter = time.Now().Add(time.Minute)
			log.Printf("security_audit_failed uid=%d retry_after=60s error=%v", uid, err)
		}
	}
	if hub.SubmitSecurityAudit != nil {
		hub.SubmitSecurityAudit(write)
	} else {
		write()
	}
}
