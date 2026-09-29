// Package tunnel carries a standard TLS stream inside WebSocket messages.
// Cloudflare terminates outer HTTPS; the inner TLS connection terminates only
// in the game backend and uses the public certificate bundled with the bridge.
package tunnel

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReadFrame bounds each newline-delimited JSON envelope before unmarshalling.
func ReadFrame(reader *bufio.Reader, maximum int) ([]byte, error) {
	var frame []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(frame)+len(part) > maximum {
			return nil, errors.New("tunnel frame too large")
		}
		frame = append(frame, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return frame, err
	}
}

type Frame struct {
	ErrorMessage           string         `json:"error_message,omitempty"`
	QueuedAt               time.Time      `json:"-"`
	ClientRelease          string         `json:"client_release,omitempty"`
	UDP                    *DatagramGrant `json:"udp_transport,omitempty"`
	LauncherCredentialsKey string         `json:"launcher_credentials_key,omitempty"`
	PeerReceipt            string         `json:"peer_receipt,omitempty"`
	Op                     string         `json:"op"`
	Channel                uint32         `json:"channel,omitempty"`
	Kind                   string         `json:"kind,omitempty"`
	Data                   []byte         `json:"data,omitempty"`
	Account                string         `json:"account,omitempty"`
	Password               string         `json:"password,omitempty"`
	ConfigHash             string         `json:"config_hash,omitempty"`
	UID                    uint64         `json:"uid,omitempty"`
	Error                  string         `json:"error,omitempty"`
	Port                   uint16         `json:"port,omitempty"`
	Value                  uint32         `json:"value,omitempty"`
}
type Conn struct {
	WS         *websocket.Conn
	reader     io.Reader
	writeMutex sync.Mutex
}

func (connection *Conn) Read(buffer []byte) (int, error) {
	for {
		if connection.reader == nil {
			messageType, reader, err := connection.WS.NextReader()
			if err != nil {
				return 0, err
			}
			if messageType != websocket.BinaryMessage {
				return 0, io.ErrUnexpectedEOF
			}
			connection.reader = reader
		}
		count, err := connection.reader.Read(buffer)
		if err == io.EOF {
			connection.reader = nil
			if count > 0 {
				return count, nil
			}
			continue
		}
		return count, err
	}
}
func (connection *Conn) Write(buffer []byte) (int, error) {
	connection.writeMutex.Lock()
	defer connection.writeMutex.Unlock()
	err := connection.WS.WriteMessage(websocket.BinaryMessage, buffer)
	if err != nil {
		return 0, err
	}
	return len(buffer), nil
}
func (connection *Conn) Close() error         { return connection.WS.Close() }
func (connection *Conn) LocalAddr() net.Addr  { return connection.WS.LocalAddr() }
func (connection *Conn) RemoteAddr() net.Addr { return connection.WS.RemoteAddr() }
func (connection *Conn) SetDeadline(deadline time.Time) error {
	if err := connection.SetReadDeadline(deadline); err != nil {
		return err
	}
	return connection.SetWriteDeadline(deadline)
}
func (connection *Conn) SetReadDeadline(deadline time.Time) error {
	return connection.WS.SetReadDeadline(deadline)
}
func (connection *Conn) SetWriteDeadline(deadline time.Time) error {
	return connection.WS.SetWriteDeadline(deadline)
}
func Certificate(directory string) (tls.Certificate, error) {
	certificatePath, keyPath := filepath.Join(directory, "origin.crt"), filepath.Join(directory, "origin.key")
	if _, err := os.Stat(certificatePath); err == nil {
		return tls.LoadX509KeyPair(certificatePath, keyPath)
	} else if !os.IsNotExist(err) {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return tls.Certificate{}, err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "kk-origin"}, DNSNames: []string{"kk-origin"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(2, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}), 0600); err != nil {
		return tls.Certificate{}, err
	}
	if err = os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certificatePath, keyPath)
}
