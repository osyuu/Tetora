package discord

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WsConn is a minimal WebSocket client connection (RFC 6455, no external deps).
type WsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex // protects writes
}

// NewWsConn wraps an existing net.Conn and bufio.Reader into a WsConn.
// Used by callers that perform their own dial/upgrade (e.g. wsConnectWithAuth, wsUpgrade).
func NewWsConn(conn net.Conn, reader *bufio.Reader) *WsConn {
	return &WsConn{conn: conn, reader: reader}
}

// WsConnect performs the WebSocket handshake over TLS.
func WsConnect(rawURL string) (*WsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	// TLS dial.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, &tls.Config{})
	if err != nil {
		return nil, fmt.Errorf("tls dial: %w", err)
	}

	// Generate WebSocket key.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	// Send HTTP upgrade request.
	path := u.RequestURI()
	reqStr := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key)
	if _, err := conn.Write([]byte(reqStr)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade: %w", err)
	}

	// Read response.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read status: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("upgrade failed: %s", strings.TrimSpace(statusLine))
	}

	// Read headers until empty line.
	expectedAccept := WsAcceptKey(key)
	gotAccept := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("read headers: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			val := strings.TrimSpace(line[len("sec-websocket-accept:"):])
			if val == expectedAccept {
				gotAccept = true
			}
		}
	}
	if !gotAccept {
		conn.Close()
		return nil, fmt.Errorf("invalid Sec-WebSocket-Accept")
	}

	return &WsConn{conn: conn, reader: reader}, nil
}

// WsAcceptKey computes the expected Sec-WebSocket-Accept value.
func WsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadJSON reads a WebSocket text frame and decodes JSON.
func (ws *WsConn) ReadJSON(v any) error {
	data, err := ws.readFrame()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON encodes JSON and sends as a WebSocket text frame.
func (ws *WsConn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.writeFrame(1, data) // opcode 1 = text
}

// Close sends a close frame and closes the connection.
func (ws *WsConn) Close() error {
	ws.writeFrame(8, nil) // opcode 8 = close
	return ws.conn.Close()
}

// readFrame reads a single WebSocket frame (handles continuation).
func (ws *WsConn) readFrame() ([]byte, error) {
	ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	var result []byte
	for {
		// Read first 2 bytes.
		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, header); err != nil {
			return nil, err
		}

		fin := header[0]&0x80 != 0
		opcode := header[0] & 0x0F
		masked := header[1]&0x80 != 0
		payloadLen := int64(header[1] & 0x7F)

		// Close frame.
		if opcode == 8 {
			return nil, io.EOF
		}

		// Ping frame — respond with pong.
		if opcode == 9 {
			pongData := make([]byte, payloadLen)
			io.ReadFull(ws.reader, pongData)
			ws.writeFrame(10, pongData) // opcode 10 = pong
			continue
		}

		// Extended payload length.
		if payloadLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint16(ext))
		} else if payloadLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return nil, err
			}
			payloadLen = int64(binary.BigEndian.Uint64(ext))
		}

		// Masking key (server frames typically aren't masked, but handle it).
		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(ws.reader, maskKey[:]); err != nil {
				return nil, err
			}
		}

		// Read payload.
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(ws.reader, payload); err != nil {
			return nil, err
		}

		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		result = append(result, payload...)

		if fin {
			break
		}
	}
	return result, nil
}

// writeFrame writes a WebSocket frame (client frames are masked per RFC 6455).
func (ws *WsConn) writeFrame(opcode byte, data []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	var frame []byte
	frame = append(frame, 0x80|opcode) // FIN + opcode

	length := len(data)
	if length < 126 {
		frame = append(frame, byte(length)|0x80) // mask bit set
	} else if length < 65536 {
		frame = append(frame, 126|0x80)
		frame = append(frame, byte(length>>8), byte(length))
	} else {
		frame = append(frame, 127|0x80)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(length))
		frame = append(frame, b...)
	}

	// Masking key.
	maskKey := make([]byte, 4)
	rand.Read(maskKey)
	frame = append(frame, maskKey...)

	// Masked payload.
	masked := make([]byte, length)
	for i := range data {
		masked[i] = data[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)

	ws.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := ws.conn.Write(frame)
	return err
}

// ReadMessage reads a single WebSocket frame and returns its opcode and payload.
// Unlike ReadJSON, this does not handle continuation frames or JSON decoding.
func (ws *WsConn) ReadMessage() (opcode int, payload []byte, err error) {
	// Read frame header (2 bytes minimum).
	header := make([]byte, 2)
	if _, err := io.ReadFull(ws.reader, header); err != nil {
		return 0, nil, err
	}

	fin := (header[0] & 0x80) != 0
	opcode = int(header[0] & 0x0F)
	masked := (header[1] & 0x80) != 0
	payloadLen := int64(header[1] & 0x7F)

	// Read extended payload length.
	if payloadLen == 126 {
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, lenBuf); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(lenBuf))
	} else if payloadLen == 127 {
		lenBuf := make([]byte, 8)
		if _, err := io.ReadFull(ws.reader, lenBuf); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(lenBuf))
	}

	// Read masking key if present.
	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(ws.reader, maskKey); err != nil {
			return 0, nil, err
		}
	}

	// Read payload.
	payload = make([]byte, payloadLen)
	if _, err := io.ReadFull(ws.reader, payload); err != nil {
		return 0, nil, err
	}

	// Unmask payload.
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	_ = fin // ignore for now (assume single-frame messages)

	// Handle control frames.
	if opcode == wsClose {
		return opcode, payload, io.EOF
	}
	if opcode == wsPing {
		// Respond with pong.
		ws.WriteMessage(wsPong, payload)
		return ws.ReadMessage() // read next message
	}

	return opcode, payload, nil
}

// WriteMessage sends a raw WebSocket frame with the given opcode and payload.
// Unlike WriteJSON, this does not mask the payload (suitable for server-side use).
func (ws *WsConn) WriteMessage(opcode int, payload []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Build frame header.
	header := []byte{0x80 | byte(opcode)} // FIN=1, opcode
	payloadLen := len(payload)

	if payloadLen < 126 {
		header = append(header, byte(payloadLen))
	} else if payloadLen <= 0xFFFF {
		header = append(header, 126)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(payloadLen))
		header = append(header, lenBuf...)
	} else {
		header = append(header, 127)
		lenBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBuf, uint64(payloadLen))
		header = append(header, lenBuf...)
	}

	// Write header and payload.
	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	if _, err := ws.conn.Write(payload); err != nil {
		return err
	}

	return nil
}

// WebSocket opcode constants used by ReadMessage/WriteMessage.
const (
	wsClose = 8
	wsPing  = 9
	wsPong  = 10
)
