package service

import (
	"bufio"
	"errors"
	"io"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Preserve wire line endings while bounding allocations for unterminated lines.
type geminiSSELineReader struct {
	reader      *bufio.Reader
	maxLineSize int
}

func newGeminiSSELineReader(reader io.Reader, cfg *config.Config) *geminiSSELineReader {
	limit := defaultMaxLineSize
	if cfg != nil && cfg.Gateway.MaxLineSize > 0 {
		limit = cfg.Gateway.MaxLineSize
	}
	return &geminiSSELineReader{reader: bufio.NewReader(reader), maxLineSize: limit}
}

func (r *geminiSSELineReader) ReadLineString() (string, error) {
	var line []byte
	for {
		part, err := r.reader.ReadSlice('\n')
		if len(part) > r.maxLineSize-len(line) {
			return "", bufio.ErrTooLong
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return string(line), err
	}
}
