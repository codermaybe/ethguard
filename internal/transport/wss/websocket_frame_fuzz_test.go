package wss

// Fuzz invariants for the frame codec: arbitrary byte streams must never
// panic the reader, never deliver a message above the limit, never deliver a
// control payload above the RFC bound, and never loop forever on a stream
// that stops making progress. The write path round-trips arbitrary payloads
// through the opposite-direction reader.

import (
	"bufio"
	"bytes"
	"testing"
)

func FuzzFrameStream(f *testing.F) {
	seeds := [][]byte{
		append([]byte{0x81, 0x05}, []byte("Hello")...),
		append([]byte{0x81, 0x85, 1, 2, 3, 4}, []byte("Hello")...),
		{0x81, 0x00},
		{0x89, 0x00},
		{0x88, 0x02, 0x03, 0xE8},
		{0x88, 0x01, 0x03},
		{0x81, 126, 0x01, 0x00},
		{0x81, 127, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00},
		{0x01, 0x01, 'a', 0x80, 0x01, 'b'},
		{0x83, 0x00},
		{0x91, 0x00},
		{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	const limit = 4096
	f.Fuzz(func(t *testing.T, data []byte) {
		reader := newFrameReader(bufio.NewReader(bytes.NewReader(data)), clientSide, limit)
		for i := 0; i < 64; i++ {
			opcode, msg, err := reader.readMessage(func(op byte, payload []byte) error {
				if len(payload) > maxControlPayload {
					t.Errorf("control payload %d exceeds the RFC bound", len(payload))
				}
				return nil
			})
			if err != nil {
				return
			}
			if len(msg) > limit {
				t.Errorf("delivered a %d-byte message above the limit %d", len(msg), limit)
			}
			if opcode != opText && opcode != opBinary {
				t.Errorf("delivered opcode %#x as a data message", opcode)
			}
		}
	})
}

func FuzzWriteFrameRoundTrip(f *testing.F) {
	f.Add([]byte("Hello"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte("x"), 125))
	f.Add(bytes.Repeat([]byte("x"), 126))
	f.Add(bytes.Repeat([]byte("x"), 65535))
	f.Add([]byte{0x00, 0xff, 0xfe, 0xfd})
	f.Fuzz(func(t *testing.T, payload []byte) {
		var buf bytes.Buffer
		if err := writeFrame(&buf, clientSide, opText, payload); err != nil {
			t.Fatalf("writeFrame: %v", err)
		}
		reader := newFrameReader(bufio.NewReader(&buf), serverSide, int64(len(payload))+16)
		frame, err := reader.readFrame()
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if frame.opcode != opText || !frame.fin || !bytes.Equal(frame.payload, payload) {
			t.Fatalf("round trip mismatch: opcode %#x fin %v len %d, want len %d", frame.opcode, frame.fin, len(frame.payload), len(payload))
		}
	})
}
