package wss

// Frame-codec tests. Raw frame bytes are built by hand in this file — not via
// writeFrame — so the reader is verified against independent encodings, and
// the writer is verified against manual unmasking.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// rawServerFrame builds the exact bytes a compliant server sends: unmasked.
func rawServerFrame(fin bool, opcode byte, payload []byte) []byte {
	return rawFrame(false, fin, opcode, payload, [4]byte{})
}

// rawClientFrame builds the exact bytes a compliant client sends: masked
// with the given key.
func rawClientFrame(fin bool, opcode byte, payload []byte, key [4]byte) []byte {
	return rawFrame(true, fin, opcode, payload, key)
}

func rawFrame(masked, fin bool, opcode byte, payload []byte, key [4]byte) []byte {
	var out []byte
	b0 := opcode
	if fin {
		b0 |= 0x80
	}
	out = append(out, b0)
	maskBit := byte(0)
	if masked {
		maskBit = 0x80
	}
	switch {
	case len(payload) <= 125:
		out = append(out, maskBit|byte(len(payload)))
	case len(payload) <= 0xFFFF:
		out = append(out, maskBit|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		out = append(out, ext[:]...)
	default:
		out = append(out, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		out = append(out, ext[:]...)
	}
	if masked {
		out = append(out, key[:]...)
		maskedPayload := make([]byte, len(payload))
		copy(maskedPayload, payload)
		maskBytes(maskedPayload, key)
		out = append(out, maskedPayload...)
		return out
	}
	return append(out, payload...)
}

// frameStream wraps raw bytes as a client-side frame reader.
func frameStream(t *testing.T, data []byte, limit int64) *frameReader {
	t.Helper()
	return newFrameReader(bufio.NewReader(bytes.NewReader(data)), clientSide, limit)
}

func readOneMessage(t *testing.T, data []byte, limit int64) (byte, []byte, error) {
	t.Helper()
	return frameStream(t, data, limit).readMessage(nil)
}

func TestReadFrameRFC6455BasicVectors(t *testing.T) {
	cases := []struct {
		name    string
		wire    []byte
		opcode  byte
		payload string
	}{
		{
			name:    "unmasked text hello",
			wire:    append([]byte{0x81, 0x05}, []byte("Hello")...),
			opcode:  opText,
			payload: "Hello",
		},
		{
			name:    "empty text",
			wire:    []byte{0x81, 0x00},
			opcode:  opText,
			payload: "",
		},
		{
			name:    "ping",
			wire:    append([]byte{0x89, 0x03}, []byte("abc")...),
			opcode:  opPing,
			payload: "abc",
		},
		{
			name:    "pong",
			wire:    []byte{0x8A, 0x00},
			opcode:  opPong,
			payload: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f, err := frameStream(t, tc.wire, 1<<20).readFrame()
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}
			if f.opcode != tc.opcode || string(f.payload) != tc.payload || !f.fin {
				t.Fatalf("opcode = %#x fin = %v data = %q, want %#x %q", f.opcode, f.fin, f.payload, tc.opcode, tc.payload)
			}
		})
	}
}

func TestReadFrameExtendedLengths(t *testing.T) {
	t.Run("16-bit length", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), 300)
		opcode, data, err := readOneMessage(t, rawServerFrame(true, opText, payload), 1<<20)
		if err != nil {
			t.Fatalf("readMessage: %v", err)
		}
		if opcode != opText || !bytes.Equal(data, payload) {
			t.Fatalf("payload mismatch (len %d)", len(data))
		}
	})
	t.Run("64-bit length", func(t *testing.T) {
		payload := bytes.Repeat([]byte("y"), 70000)
		opcode, data, err := readOneMessage(t, rawServerFrame(true, opText, payload), 1<<20)
		if err != nil {
			t.Fatalf("readMessage: %v", err)
		}
		if opcode != opText || !bytes.Equal(data, payload) {
			t.Fatalf("payload mismatch (len %d)", len(data))
		}
	})
}

func TestReadFrameRejectsProtocolViolations(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
	}{
		{name: "masked server frame", wire: rawClientFrame(true, opText, []byte("hi"), [4]byte{1, 2, 3, 4})},
		{name: "rsv1 set", wire: []byte{0x91, 0x00}},
		{name: "rsv2 set", wire: []byte{0xA1, 0x00}},
		{name: "rsv3 set", wire: []byte{0xC1, 0x00}},
		{name: "undefined opcode 0x3", wire: []byte{0x83, 0x00}},
		{name: "undefined opcode 0xF", wire: []byte{0x8F, 0x00}},
		{name: "fragmented ping", wire: []byte{0x09, 0x00}},
		{name: "fragmented close", wire: []byte{0x08, 0x00}},
		{name: "oversized ping", wire: append([]byte{0x89, 126, 0x00, 0x7E}, bytes.Repeat([]byte("z"), 126)...)},
		{name: "control frame over 125 bytes", wire: append([]byte{0x89, 126, 0x00, 0x80}, bytes.Repeat([]byte("z"), 128)...)},
		{name: "64-bit length high bit", wire: append([]byte{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 0}, bytes.Repeat([]byte("b"), 8)...)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := readOneMessage(t, tc.wire, 1<<20)
			var frameErr *frameError
			if !errors.As(err, &frameErr) {
				t.Fatalf("err = %v, want a frame protocol error", err)
			}
			if !errors.Is(err, ErrFrameProtocol) || frameErr.code != closeProtocolError {
				t.Fatalf("err = %v, want close-code 1002 classification", err)
			}
		})
	}
}

func TestReadFrameRefusesOversizedFramesBeforeReadingPayload(t *testing.T) {
	// The header announces a 1000-byte payload, the limit is 100, and the
	// stream carries no payload bytes at all: the reader must fail on the
	// limit, not block or EOF waiting for the body.
	wire := []byte{0x81, 126, 0x03, 0xE8}
	if _, _, err := readOneMessage(t, wire, 100); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want the frame-too-large sentinel", err)
	}
	// A 64-bit announced length far beyond the limit fails the same way
	// without reading the body.
	wire = []byte{0x81, 127, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00}
	if _, _, err := readOneMessage(t, wire, 100); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want the frame-too-large sentinel", err)
	}
}

func TestReadFrameTruncatedInputFailsClosed(t *testing.T) {
	full := rawServerFrame(true, opText, []byte("0123456789"))
	cases := []struct {
		name string
		wire []byte
	}{
		{name: "header cut", wire: full[:1]},
		{name: "extended length cut", wire: []byte{0x81, 126, 0x00}},
		{name: "payload cut", wire: full[:len(full)-3]},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, data, err := readOneMessage(t, tc.wire, 1<<20)
			if err == nil {
				t.Fatalf("delivered %q from a truncated frame", data)
			}
			if errors.Is(err, ErrFrameProtocol) {
				t.Fatalf("truncation is a stream failure, not a protocol error: %v", err)
			}
		})
	}
}

func TestFragmentedMessageAssembly(t *testing.T) {
	var wire []byte
	wire = append(wire, rawServerFrame(false, opText, []byte("Hel"))...)
	wire = append(wire, rawServerFrame(false, opContinuation, []byte("lo "))...)
	wire = append(wire, rawServerFrame(true, opContinuation, []byte("world"))...)
	opcode, data, err := readOneMessage(t, wire, 1<<20)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if opcode != opText || string(data) != "Hello world" {
		t.Fatalf("opcode = %#x data = %q, want the assembled message", opcode, data)
	}
}

func TestFragmentedMessageRejectsBrokenSequences(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
	}{
		{
			name: "new data frame inside a fragmented message",
			wire: append(
				rawServerFrame(false, opText, []byte("a")),
				rawServerFrame(true, opText, []byte("b"))...,
			),
		},
		{
			name: "continuation with no message in progress",
			wire: rawServerFrame(true, opContinuation, []byte("a")),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := readOneMessage(t, tc.wire, 1<<20)
			var frameErr *frameError
			if !errors.As(err, &frameErr) || frameErr.code != closeProtocolError {
				t.Fatalf("err = %v, want a 1002 protocol error", err)
			}
		})
	}
}

func TestControlFrameDeliveredBetweenFragments(t *testing.T) {
	var wire []byte
	wire = append(wire, rawServerFrame(false, opText, []byte("a"))...)
	wire = append(wire, rawServerFrame(true, opPing, []byte("probe"))...)
	wire = append(wire, rawServerFrame(true, opContinuation, []byte("b"))...)
	var controls []byte
	opcode, data, err := frameStream(t, wire, 1<<20).readMessage(func(op byte, payload []byte) error {
		controls = append(controls, op)
		if string(payload) != "probe" {
			t.Errorf("control payload = %q, want probe", payload)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if opcode != opText || string(data) != "ab" {
		t.Fatalf("message = %#x %q, want the assembled text", opcode, data)
	}
	if len(controls) != 1 || controls[0] != opPing {
		t.Fatalf("controls = %v, want one ping", controls)
	}
}

func TestTextMessagesMustBeValidUTF8(t *testing.T) {
	validSplit := append(
		rawServerFrame(false, opText, []byte{0xE6, 0xBC}),
		rawServerFrame(true, opContinuation, []byte{0xA2})...,
	)
	if opcode, data, err := readOneMessage(t, validSplit, 1<<20); err != nil || opcode != opText || string(data) != "漢" {
		t.Fatalf("a codepoint split across fragments must assemble: %v %q", err, data)
	}
	if _, _, err := readOneMessage(t, rawServerFrame(true, opText, []byte{0xFF, 0xFE}), 1<<20); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	} else {
		var frameErr *frameError
		if !errors.As(err, &frameErr) || frameErr.code != closeInvalidPayloadData {
			t.Fatalf("err = %v, want a 1007 classification", err)
		}
	}
	invalidSplit := append(
		rawServerFrame(false, opText, []byte{0xE6, 0xBC}),
		rawServerFrame(true, opContinuation, []byte{0xFF})...,
	)
	if _, _, err := readOneMessage(t, invalidSplit, 1<<20); err == nil {
		t.Fatal("invalid UTF-8 across fragments accepted")
	}
}

func TestBinaryDataMessagesAreRefused(t *testing.T) {
	_, _, err := readOneMessage(t, rawServerFrame(true, opBinary, []byte{0x00, 0x01}), 1<<20)
	var frameErr *frameError
	if !errors.As(err, &frameErr) || frameErr.code != closeUnsupportedData {
		t.Fatalf("err = %v, want a 1003 classification", err)
	}
}

func TestMessageLimitAppliesAcrossFragments(t *testing.T) {
	var wire []byte
	wire = append(wire, rawServerFrame(false, opText, bytes.Repeat([]byte("a"), 90))...)
	wire = append(wire, rawServerFrame(true, opContinuation, bytes.Repeat([]byte("b"), 20))...)
	if _, _, err := readOneMessage(t, wire, 100); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want the frame-too-large sentinel", err)
	}
}

func TestWriteFrameRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 125, 126, 300, 0xFFFF, 0xFFFF + 1, 70000}
	for _, size := range sizes {
		payload := bytes.Repeat([]byte("p"), size)
		var buf bytes.Buffer
		if err := writeFrame(&buf, clientSide, opText, payload); err != nil {
			t.Fatalf("writeFrame(%d): %v", size, err)
		}
		reader := newFrameReader(bufio.NewReader(&buf), serverSide, 1<<20)
		f, err := reader.readFrame()
		if err != nil {
			t.Fatalf("readFrame(%d): %v", size, err)
		}
		if f.opcode != opText || !f.fin || !bytes.Equal(f.payload, payload) {
			t.Fatalf("round trip mismatch at size %d (opcode %#x fin %v len %d)", size, f.opcode, f.fin, len(f.payload))
		}
	}
}

func TestWriteFrameRejectsUnwritableFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, clientSide, opContinuation, nil); err == nil {
		t.Error("continuation frames are not written by the raw-call path")
	}
	if err := writeFrame(&buf, clientSide, opPing, bytes.Repeat([]byte("x"), 126)); err == nil {
		t.Error("control frames over 125 bytes must be refused")
	}
	if err := writeFrame(&buf, clientSide, 0x3, nil); err == nil {
		t.Error("undefined opcodes must be refused")
	}
}

func TestWriteFrameMasksWithFreshKeys(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("sensitive")
	if err := writeFrame(&buf, clientSide, opText, payload); err != nil {
		t.Fatal(err)
	}
	wire := buf.Bytes()
	if wire[0] != 0x81 {
		t.Fatalf("first byte = %#x, want fin+text", wire[0])
	}
	if wire[1]&0x80 == 0 {
		t.Fatal("client frame is not masked")
	}
	var key [4]byte
	copy(key[:], wire[2:6])
	masked := wire[6:]
	if bytes.Equal(masked, payload) {
		t.Fatal("payload written unmasked")
	}
	plain := make([]byte, len(masked))
	copy(plain, masked)
	maskBytes(plain, key)
	if string(plain) != string(payload) {
		t.Fatalf("unmasked payload = %q, want %q", plain, payload)
	}
}

func TestParseCloseFrame(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    CloseError
		wantErr bool
	}{
		{name: "code only", payload: []byte{0x03, 0xE8}, want: CloseError{Code: 1000}},
		{name: "code with reason", payload: append([]byte{0x03, 0xE9}, []byte("bye")...), want: CloseError{Code: 1001, Reason: "bye"}},
		{name: "registered range", payload: []byte{0x0B, 0xB8}, want: CloseError{Code: 3000}},
		{name: "top of registered range", payload: []byte{0x13, 0x87}, want: CloseError{Code: 4999}},
		{name: "no status", payload: nil, want: CloseError{Code: closeNoStatusReceived}},
		{name: "one byte", payload: []byte{0x03}, wantErr: true},
		{name: "invalid code 1004", payload: []byte{0x03, 0xEC}, wantErr: true},
		{name: "invalid code 1005", payload: []byte{0x03, 0xED}, wantErr: true},
		{name: "invalid code 1006", payload: []byte{0x03, 0xEE}, wantErr: true},
		{name: "invalid code 1015", payload: []byte{0x03, 0xF7}, wantErr: true},
		{name: "invalid code 2999", payload: []byte{0x0B, 0xB7}, wantErr: true},
		{name: "invalid code 5000", payload: []byte{0x13, 0x88}, wantErr: true},
		{name: "invalid code zero", payload: []byte{0x00, 0x00}, wantErr: true},
		{name: "non-utf8 reason", payload: []byte{0x03, 0xE8, 0xFF, 0xFE}, wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCloseFrame(tc.payload)
			if tc.wantErr {
				var frameErr *frameError
				if !errors.As(err, &frameErr) {
					t.Fatalf("err = %v, want a frame protocol error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCloseFrame: %v", err)
			}
			if got.Code != tc.want.Code || got.Reason != tc.want.Reason {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFrameStreamFailsClosedAfterMalformedFrame(t *testing.T) {
	var wire []byte
	wire = append(wire, rawServerFrame(true, opText, []byte("fine"))...)
	wire = append(wire, 0x83, 0x00) // undefined opcode
	wire = append(wire, rawServerFrame(true, opText, []byte("after"))...)
	reader := frameStream(t, wire, 1<<20)
	opcode, data, err := reader.readMessage(nil)
	if err != nil || opcode != opText || string(data) != "fine" {
		t.Fatalf("first message: %v %q", err, data)
	}
	if _, _, err := reader.readMessage(nil); !errors.Is(err, ErrFrameProtocol) {
		t.Fatalf("second read = %v, want the protocol sentinel", err)
	}
	if _, _, err := reader.readMessage(nil); !errors.Is(err, ErrFrameProtocol) {
		t.Fatal("a broken stream must not resynchronize")
	}
}

// errorCategory reduces a read failure to the comparable kind the sticky
// contract is asserted on.
func errorCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	default:
		var frameErr *frameError
		if errors.As(err, &frameErr) {
			return fmt.Sprintf("frame-error-%d", frameErr.code)
		}
		if errors.Is(err, ErrFrameTooLarge) {
			return "too-large"
		}
		var closeErr CloseError
		if errors.As(err, &closeErr) {
			return fmt.Sprintf("close-%d", closeErr.Code)
		}
		return "other"
	}
}

// Every message-level failure must be sticky: the second read returns the
// same failure category, never the decoy frame that follows on the wire.
func TestReadMessageFailureIsSticky(t *testing.T) {
	decoy := rawServerFrame(true, opText, []byte("decoy"))
	failingUTF8 := rawServerFrame(true, opText, []byte{0xFF, 0xFE})
	failingUTF8Split := append(
		rawServerFrame(false, opText, []byte{0xE6, 0xBC}),
		rawServerFrame(true, opContinuation, []byte{0xFF})...,
	)
	danglingContinuation := rawServerFrame(true, opContinuation, []byte("a"))
	newDataMidMessage := append(
		rawServerFrame(false, opText, []byte("a")),
		rawServerFrame(true, opText, []byte("b"))...,
	)
	overLimitMessage := append(
		rawServerFrame(false, opText, bytes.Repeat([]byte("a"), 90)),
		rawServerFrame(true, opContinuation, bytes.Repeat([]byte("b"), 20))...,
	)
	binaryMessage := rawServerFrame(true, opBinary, []byte{0x01})
	invalidClose := []byte{0x88, 0x01, 0x03} // one-byte close payload
	validClose := append([]byte{0x88, 0x05, 0x03, 0xE8}, []byte("bye")...)

	// mimicControl mirrors the connection's control handling: close frames
	// are parsed (parse failure is a protocol error) and valid closes
	// surface as CloseError.
	mimicControl := func(_ byte, payload []byte) error {
		closeFrame, err := parseCloseFrame(payload)
		if err != nil {
			return err
		}
		return closeFrame
	}

	cases := []struct {
		name         string
		wire         []byte
		limit        int64
		onControl    func(byte, []byte) error
		wantCategory string
	}{
		{name: "invalid utf-8 single frame", wire: append(failingUTF8, decoy...), wantCategory: "frame-error-1007"},
		{name: "invalid utf-8 across fragments", wire: append(failingUTF8Split, decoy...), wantCategory: "frame-error-1007"},
		{name: "dangling continuation", wire: append(danglingContinuation, decoy...), wantCategory: "frame-error-1002"},
		{name: "new data frame mid-message", wire: append(newDataMidMessage, decoy...), wantCategory: "frame-error-1002"},
		{name: "message limit across fragments", wire: append(overLimitMessage, decoy...), limit: 100, wantCategory: "too-large"},
		{name: "binary message refused", wire: append(binaryMessage, decoy...), wantCategory: "frame-error-1003"},
		{name: "invalid close payload", wire: append(invalidClose, decoy...), onControl: mimicControl, wantCategory: "frame-error-1002"},
		{name: "valid close via control callback", wire: append(validClose, decoy...), onControl: mimicControl, wantCategory: "close-1000"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reader := frameStream(t, tc.wire, tc.limit)
			_, _, first := reader.readMessage(tc.onControl)
			if got := errorCategory(first); got != tc.wantCategory {
				t.Fatalf("first read = %v (category %q), want %q", first, got, tc.wantCategory)
			}
			_, _, second := reader.readMessage(tc.onControl)
			if got := errorCategory(second); got != tc.wantCategory {
				t.Fatalf("second read = %v (category %q), want the sticky %q, not a resynchronized frame", second, got, tc.wantCategory)
			}
			if !errors.Is(second, first) {
				t.Fatalf("second read %v is not the recorded first failure %v", second, first)
			}
		})
	}
}

// The wire form must never carry an invalid UTF-8 reason into the error text.
func TestCloseErrorTextIsPrintable(t *testing.T) {
	e := CloseError{Code: closeNormalClosure, Reason: "going away"}
	if !strings.Contains(e.Error(), "1000") || !strings.Contains(e.Error(), "going away") {
		t.Fatalf("text = %q", e.Error())
	}
	if !utf8.ValidString(e.Error()) {
		t.Fatal("error text is not valid UTF-8")
	}
}
