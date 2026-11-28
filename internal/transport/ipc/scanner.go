package ipc

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/transport"
)

var ErrInvalidMessage = errors.New("ipc: invalid message boundary")
var ErrBatchUnsupported = errors.New("ipc: JSON-RPC batch is unsupported")

// scanner locates object boundaries only; rpcwire owns JSON-RPC validation.
// Its fixed read buffer retains adjacent messages, while each returned object
// owns a bounded allocation that no subsequent read can overwrite.
type scanner struct {
	reader   *bufio.Reader
	limit    int64
	terminal error
}

func newScanner(r io.Reader, limit int64) *scanner {
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	return &scanner{reader: bufio.NewReaderSize(r, 4096), limit: limit}
}

func (s *scanner) readMessage() ([]byte, error) {
	if s.terminal != nil {
		return nil, s.terminal
	}
	message, err := s.scan()
	if err != nil {
		s.terminal = err
	}
	return message, err
}

func (s *scanner) scan() ([]byte, error) {
	var first byte
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		first = b
		break
	}
	if first == '[' {
		return nil, ErrBatchUnsupported
	}
	if first != '{' {
		return nil, fmt.Errorf("%w: expected an object", ErrInvalidMessage)
	}
	var stack [jsonsafe.MaxNestingDepth]byte
	stack[0] = '{'
	depth := 1
	quoted, escaped := false, false
	out := make([]byte, 1, min(int64(256), s.limit))
	out[0] = first
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if int64(len(out)) >= s.limit {
			return nil, transport.ErrBodyTooLarge
		}
		if len(out) == cap(out) {
			next := make([]byte, len(out), min(int64(cap(out))*2, s.limit))
			copy(next, out)
			out = next
		}
		out = append(out, b)
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			if depth == len(stack) {
				return nil, jsonsafe.ErrTooDeep
			}
			stack[depth] = b
			depth++
		case '}', ']':
			want := byte('{')
			if b == ']' {
				want = '['
			}
			if stack[depth-1] != want {
				return nil, fmt.Errorf("%w: mismatched container", ErrInvalidMessage)
			}
			depth--
			if depth == 0 {
				return out, nil
			}
		}
	}
}
