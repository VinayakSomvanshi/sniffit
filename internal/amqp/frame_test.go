package amqp

import (
	"encoding/binary"
	"testing"
)

func TestFrame_UnmarshalMethod_Success(t *testing.T) {
	// Payload: Class 10 (Connection), Method 10 (Start), Args [0x01, 0x02]
	payload := make([]byte, 6)
	binary.BigEndian.PutUint16(payload[0:2], 10)
	binary.BigEndian.PutUint16(payload[2:4], 10)
	payload[4] = 0x01
	payload[5] = 0x02

	frame := &Frame{
		Type:    FrameMethod,
		Payload: payload,
	}

	method, err := frame.UnmarshalMethod()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if method.ClassID != 10 {
		t.Errorf("Expected ClassID 10, got %d", method.ClassID)
	}
	if method.MethodID != 10 {
		t.Errorf("Expected MethodID 10, got %d", method.MethodID)
	}
	if len(method.Args) != 2 || method.Args[0] != 0x01 {
		t.Errorf("Unexpected args: %v", method.Args)
	}
}

func TestFrame_UnmarshalMethod_Error(t *testing.T) {
	// Wrong frame type
	frame := &Frame{Type: FrameHeader}
	_, err := frame.UnmarshalMethod()
	if err == nil {
		t.Fatal("Expected error for non-method frame")
	}

	// Payload too small
	frame = &Frame{Type: FrameMethod, Payload: []byte{0x00, 0x0A}}
	_, err = frame.UnmarshalMethod()
	if err == nil {
		t.Fatal("Expected error for small payload")
	}
}
