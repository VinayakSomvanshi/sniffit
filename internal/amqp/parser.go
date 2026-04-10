package amqp

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Parser reads from an active stream and outputs AMQP Frames
type Parser struct {
	reader io.Reader
	buf    []byte
}

// NewParser creates a new AMQP frame parser over an io.Reader
func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: r,
		buf:    make([]byte, 0, 4096),
	}
}

// NextFrame reads the next AMQP frame from the stream
func (p *Parser) NextFrame() (*Frame, error) {
	header := make([]byte, 7)
	_, err := io.ReadFull(p.reader, header)
	if err != nil {
		return nil, err
	}

	// 1 byte: Type
	// 2 bytes: Channel
	// 4 bytes: Size
	typ := header[0]
	channel := binary.BigEndian.Uint16(header[1:3])
	size := binary.BigEndian.Uint32(header[3:7])

	payload := make([]byte, size)
	_, err = io.ReadFull(p.reader, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload: %w", err)
	}

	endByte := make([]byte, 1)
	_, err = io.ReadFull(p.reader, endByte)
	if err != nil {
		return nil, fmt.Errorf("failed to read end byte: %w", err)
	}

	if endByte[0] != FrameEndByte {
		return nil, fmt.Errorf("invalid frame end byte: %x", endByte[0])
	}

	return &Frame{
		Type:    typ,
		Channel: channel,
		Size:    size,
		Payload: payload,
	}, nil
}
