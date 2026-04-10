package amqp

import (
	"encoding/binary"
	"fmt"
)

const (
	FrameMethod    = 1
	FrameHeader    = 2
	FrameBody      = 3
	FrameHeartbeat = 8
	FrameEndByte   = 0xCE
)

// Frame represents a decoded AMQP 0-9-1 Frame
type Frame struct {
	Type    uint8
	Channel uint16
	Size    uint32
	Payload []byte
}

// MethodPayload represents the inner payload of a Method Frame
type MethodPayload struct {
	ClassID  uint16
	MethodID uint16
	Args     []byte
}

// UnmarshalMethod parses a method frame's payload
func (f *Frame) UnmarshalMethod() (*MethodPayload, error) {
	if f.Type != FrameMethod {
		return nil, fmt.Errorf("not a method frame")
	}
	if len(f.Payload) < 4 {
		return nil, fmt.Errorf("payload too small for method frame")
	}
	return &MethodPayload{
		ClassID:  binary.BigEndian.Uint16(f.Payload[0:2]),
		MethodID: binary.BigEndian.Uint16(f.Payload[2:4]),
		Args:     f.Payload[4:],
	}, nil
}
