package wss

// The RFC 6455 frame codec: header parsing, endpoint-direction masking,
// fragmentation, UTF-8 validation, close-frame semantics, and the two-level
// frame/message size bound. One codec serves both directions — the client
// connection reads unmasked server frames and writes masked client frames;
// the test peers use the same codec from the server side. Malformed input
// fails closed: a frame that cannot be parsed in full is never delivered to
// the JSON-RPC layer (docs/design/wss-wire-contract.md §2.2).

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Data and control opcodes (RFC 6455 §5.2).
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Close codes this implementation sends or synthesizes (RFC 6455 §7.4).
const (
	closeNormalClosure      = 1000
	closeGoingAway          = 1001
	closeProtocolError      = 1002
	closeUnsupportedData    = 1003
	closeNoStatusReceived   = 1005
	closeAbnormalClosure    = 1006
	closeInvalidPayloadData = 1007
	closeMessageTooBig      = 1009
	closeTLSHandshake       = 1015
)

// maxControlPayload is the RFC 6455 §5.5 bound for one control frame.
const maxControlPayload = 125

// ErrFrameProtocol marks a frame the peer must not have sent: a malformed
// header, an invalid opcode or mask direction, a broken fragmentation
// sequence, an invalid close code, or an invalid UTF-8 payload.
var ErrFrameProtocol = errors.New("wss: frame protocol violation")

// ErrFrameTooLarge marks an inbound frame or message above the read limit.
// Unlike a protocol violation it never produces a close frame: the
// connection is aborted directly, and the adapter classifies it as a
// body-too-large failure.
var ErrFrameTooLarge = errors.New("wss: inbound frame exceeds the read limit")

// frameError is one protocol violation carrying the close code the
// connection answers with before terminating.
type frameError struct {
	code   int
	detail string
}

func (e *frameError) Error() string {
	return fmt.Sprintf("%s (close %d): %s", ErrFrameProtocol, e.code, e.detail)
}

func (e *frameError) Unwrap() error { return ErrFrameProtocol }

// CloseError reports a close frame the peer sent. A close without a status
// code arrives as code 1005; an aborted connection never produces a
// CloseError — transport failures are classified separately.
type CloseError struct {
	Code   int
	Reason string
}

func (e CloseError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("wss: peer closed the connection (code %d)", e.Code)
	}
	return fmt.Sprintf("wss: peer closed the connection (code %d): %s", e.Code, e.Reason)
}

// endpointSide selects the mask direction each side of the wire speaks: the
// client sends masked and reads unmasked frames, the server the reverse.
type endpointSide int

const (
	clientSide endpointSide = iota
	serverSide
)

func (s endpointSide) sendsMasked() bool { return s == clientSide }

// frame is one decoded wire frame. Payloads are unmasked copies.
type frame struct {
	opcode  byte
	fin     bool
	payload []byte
}

func isControlOpcode(opcode byte) bool {
	return opcode&0x8 != 0
}

func validOpcode(opcode byte) bool {
	switch opcode {
	case opContinuation, opText, opBinary, opClose, opPing, opPong:
		return true
	}
	return false
}

// frameReader decodes one frame stream. It is not safe for concurrent use:
// the connection's single reader goroutine owns it for the connection's
// lifetime. The reader is sticky after its first error — a stream that
// produced one malformed frame can never be trusted to resynchronize, so
// every later read returns the recorded failure. limit bounds one frame and
// one assembled message; zero or less means unbounded.
type frameReader struct {
	br    *bufio.Reader
	side  endpointSide
	limit int64
	err   error
}

func newFrameReader(br *bufio.Reader, side endpointSide, limit int64) *frameReader {
	return &frameReader{br: br, side: side, limit: limit}
}

// readFrame decodes one complete frame. An error from the stream itself
// (io.EOF, io.ErrUnexpectedEOF) reports a truncated or closed connection; a
// *frameError reports a frame the peer violated the protocol with;
// ErrFrameTooLarge reports a frame above the read limit, refused before any
// payload byte is read.
func (r *frameReader) readFrame() (frame, error) {
	if r.err != nil {
		return frame{}, r.err
	}
	f, err := r.readFrameOnce()
	if err != nil {
		r.err = err
		return frame{}, err
	}
	return f, nil
}

func (r *frameReader) readFrameOnce() (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r.br, header[:]); err != nil {
		return frame{}, err
	}
	fin := header[0]&0x80 != 0
	if rsv := header[0] & 0x70; rsv != 0 {
		return frame{}, &frameError{code: closeProtocolError, detail: "RSV bits set without a negotiated extension"}
	}
	opcode := header[0] & 0x0F
	if !validOpcode(opcode) {
		return frame{}, &frameError{code: closeProtocolError, detail: fmt.Sprintf("opcode %#x is not defined", opcode)}
	}
	masked := header[1]&0x80 != 0
	// The side that sends masked reads unmasked frames and vice versa.
	if masked == r.side.sendsMasked() {
		return frame{}, &frameError{code: closeProtocolError, detail: "frame mask direction does not match the endpoint side"}
	}
	length := int64(header[1] & 0x7F)
	if isControlOpcode(opcode) && (!fin || length > maxControlPayload) {
		return frame{}, &frameError{code: closeProtocolError, detail: "control frames must be unfragmented and at most 125 bytes"}
	}
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r.br, ext[:]); err != nil {
			return frame{}, err
		}
		raw := binary.BigEndian.Uint64(ext[:])
		if raw&(1<<63) != 0 {
			return frame{}, &frameError{code: closeProtocolError, detail: "64-bit payload length has the high bit set"}
		}
		length = int64(raw)
	}
	if r.limit > 0 && length > r.limit {
		return frame{}, fmt.Errorf("%w: frame payload %d exceeds the limit %d", ErrFrameTooLarge, length, r.limit)
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r.br, maskKey[:]); err != nil {
			return frame{}, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r.br, payload); err != nil {
		return frame{}, err
	}
	if masked {
		maskBytes(payload, maskKey)
	}
	return frame{opcode: opcode, fin: fin, payload: payload}, nil
}

// readMessage decodes one complete data message, reassembling fragments.
// Control frames met before the message completes are handed to onControl —
// the connection answers pings and observes closes there — and never appear
// as message payloads. Text messages are validated as UTF-8 after assembly;
// binary data messages are refused: the raw-call contract carries JSON text
// only. Like readFrame it is sticky after its first error: every
// message-level failure (UTF-8, fragmentation, message limit, control
// handling) is a broken stream, and a later read returns the recorded
// failure instead of parsing whatever follows.
func (r *frameReader) readMessage(onControl func(opcode byte, payload []byte) error) (byte, []byte, error) {
	if r.err != nil {
		return 0, nil, r.err
	}
	opcode, data, err := r.readMessageOnce(onControl)
	if err != nil {
		r.err = err
		return 0, nil, err
	}
	return opcode, data, nil
}

func (r *frameReader) readMessageOnce(onControl func(opcode byte, payload []byte) error) (byte, []byte, error) {
	var assembled []byte
	started := false
	var startOpcode byte
	for {
		f, err := r.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch f.opcode {
		case opText, opBinary:
			if started {
				return 0, nil, &frameError{code: closeProtocolError, detail: "new data frame inside a fragmented message"}
			}
			started = true
			startOpcode = f.opcode
			if overMessageLimit(r.limit, assembled, f.payload) {
				return 0, nil, fmt.Errorf("%w: message exceeds the limit %d", ErrFrameTooLarge, r.limit)
			}
			assembled = append(assembled, f.payload...)
			if f.fin {
				if err := validateDataMessage(startOpcode, assembled); err != nil {
					return 0, nil, err
				}
				return startOpcode, assembled, nil
			}
		case opContinuation:
			if !started {
				return 0, nil, &frameError{code: closeProtocolError, detail: "continuation frame with no message in progress"}
			}
			if overMessageLimit(r.limit, assembled, f.payload) {
				return 0, nil, fmt.Errorf("%w: message exceeds the limit %d", ErrFrameTooLarge, r.limit)
			}
			assembled = append(assembled, f.payload...)
			if f.fin {
				if err := validateDataMessage(startOpcode, assembled); err != nil {
					return 0, nil, err
				}
				return startOpcode, assembled, nil
			}
		default:
			if onControl == nil {
				continue
			}
			if err := onControl(f.opcode, f.payload); err != nil {
				return 0, nil, err
			}
		}
	}
}

// overMessageLimit reports whether appending next would push the assembled
// message past the read limit. A limit of zero or less is unbounded.
func overMessageLimit(limit int64, assembled, next []byte) bool {
	if limit <= 0 {
		return false
	}
	return int64(len(assembled)+len(next)) > limit
}

// validateDataMessage applies the message-level checks the codec owns:
// text must be valid UTF-8, and binary data messages are outside the
// raw-call contract.
func validateDataMessage(opcode byte, data []byte) error {
	switch opcode {
	case opText:
		if !utf8.Valid(data) {
			return &frameError{code: closeInvalidPayloadData, detail: "text message is not valid UTF-8"}
		}
		return nil
	case opBinary:
		return &frameError{code: closeUnsupportedData, detail: "binary data messages are outside the raw-call contract"}
	}
	return &frameError{code: closeProtocolError, detail: fmt.Sprintf("data message of opcode %#x is not deliverable", opcode)}
}

// parseCloseFrame decodes a close frame payload into its code and reason. An
// empty payload means the peer sent no status (code 1005); every other
// malformed form is a protocol violation.
func parseCloseFrame(payload []byte) (CloseError, error) {
	switch {
	case len(payload) == 0:
		return CloseError{Code: closeNoStatusReceived}, nil
	case len(payload) == 1:
		return CloseError{}, &frameError{code: closeProtocolError, detail: "close frame payload of one byte"}
	}
	code := int(binary.BigEndian.Uint16(payload))
	if !validReceivedCloseCode(code) {
		return CloseError{}, &frameError{code: closeProtocolError, detail: fmt.Sprintf("close code %d is not a valid received close code", code)}
	}
	reason := payload[2:]
	if !utf8.Valid(reason) {
		return CloseError{}, &frameError{code: closeInvalidPayloadData, detail: "close reason is not valid UTF-8"}
	}
	return CloseError{Code: code, Reason: string(reason)}, nil
}

// validReceivedCloseCode mirrors RFC 6455 §7.4 and the IANA registry: the
// defined protocol codes that may cross the wire (1004/1005/1006/1015 are
// synthesized states, never wire values), plus the 3000–4999 registered-use
// range. Codes outside the set are protocol violations.
func validReceivedCloseCode(code int) bool {
	switch code {
	case 1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 1012, 1013, 1014:
		return true
	}
	return code >= 3000 && code <= 4999
}

// writeFrame encodes one single-frame message (outbound fragmentation is not
// part of the raw-call contract). The client side masks with a fresh random
// key per frame.
func writeFrame(w io.Writer, side endpointSide, opcode byte, payload []byte) error {
	if !validOpcode(opcode) || opcode == opContinuation {
		return fmt.Errorf("wss: opcode %#x is not a writable frame type", opcode)
	}
	if isControlOpcode(opcode) && len(payload) > maxControlPayload {
		return fmt.Errorf("wss: control frame payload %d exceeds the %d-byte bound", len(payload), maxControlPayload)
	}
	var header [14]byte
	header[0] = 0x80 | opcode
	n := 1
	maskBit := byte(0)
	if side.sendsMasked() {
		maskBit = 0x80
	}
	switch {
	case len(payload) <= 125:
		header[n] = maskBit | byte(len(payload))
		n++
	case len(payload) <= 0xFFFF:
		header[n] = maskBit | 126
		n++
		binary.BigEndian.PutUint16(header[n:], uint16(len(payload)))
		n += 2
	default:
		header[n] = maskBit | 127
		n++
		binary.BigEndian.PutUint64(header[n:], uint64(len(payload)))
		n += 8
	}
	if side.sendsMasked() {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return fmt.Errorf("wss: generate frame mask: %w", err)
		}
		copy(header[n:], mask[:])
		n += 4
		masked := make([]byte, len(payload))
		copy(masked, payload)
		maskBytes(masked, mask)
		if len(masked) <= 4096 {
			buf := make([]byte, 0, n+len(masked))
			buf = append(buf, header[:n]...)
			buf = append(buf, masked...)
			_, err := w.Write(buf)
			return err
		}
		if _, err := w.Write(header[:n]); err != nil {
			return err
		}
		_, err := w.Write(masked)
		return err
	}
	if _, err := w.Write(header[:n]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// maskBytes applies the RFC 6455 §5.3 masking operation in place.
func maskBytes(b []byte, key [4]byte) {
	for i := range b {
		b[i] ^= key[i&3]
	}
}
