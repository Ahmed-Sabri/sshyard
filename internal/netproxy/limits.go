package netproxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

const maxHTTPHeaderBytes = 32 << 10

// ErrHandshakeTooLarge reports a rejected handshake exceeding its wire limit.
var ErrHandshakeTooLarge = errors.New("proxy: handshake too large")

// Read only the bounded header before net/http parses it; the returned reader
// retains buffered tunnel data and streams request bodies without a size cap.
func readHTTPHeader(r *bufio.Reader) (*bufio.Reader, error) {
	header := make([]byte, 0, 1024)
	lineStart := 0
	for len(header) < maxHTTPHeaderBytes {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		header = append(header, b)
		if b != '\n' {
			continue
		}
		line := header[lineStart:]
		if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
			return bufio.NewReader(io.MultiReader(bytes.NewReader(header), r)), nil
		}
		lineStart = len(header)
	}
	return nil, ErrHandshakeTooLarge
}
