package amqp

import (
	"bytes"
	"io"
	"testing"
)

func TestParser_NextFrame_Success(t *testing.T) {
	// Sample frame: Type 1 (Method), Channel 5, Size 4, Payload [1, 2, 3, 4], End 0xCE
	data := []byte{
		0x01,                   // Type
		0x00, 0x05,             // Channel
		0x00, 0x00, 0x00, 0x04, // Size
		0x01, 0x02, 0x03, 0x04, // Payload
		0xCE,                   // End
	}

	parser := NewParser(bytes.NewReader(data))
	frame, err := parser.NextFrame()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if frame.Type != 1 {
		t.Errorf("Expected Type 1, got %d", frame.Type)
	}
	if frame.Channel != 5 {
		t.Errorf("Expected Channel 5, got %d", frame.Channel)
	}
	if frame.Size != 4 {
		t.Errorf("Expected Size 4, got %d", frame.Size)
	}
	if !bytes.Equal(frame.Payload, []byte{1, 2, 3, 4}) {
		t.Errorf("Unexpected payload: %v", frame.Payload)
	}
}

func TestParser_NextFrame_OOMPrevention(t *testing.T) {
	// Malicious frame: Size = 2MB (exceeds 1MB limit)
	data := []byte{
		0x01,                   // Type
		0x00, 0x01,             // Channel
		0x00, 0x10, 0x00, 0x00, // Size: 1,048,576 * 1 (Actually 1MB is 0x100000, 2MB is 0x200000)
		// Let's use 0x00 0x10 0x00 0x01 which is 1,048,577 (> MaxFrameSize)
	}
	data[3] = 0x00
	data[4] = 0x10
	data[5] = 0x00
	data[6] = 0x01

	parser := NewParser(bytes.NewReader(data))
	_, err := parser.NextFrame()
	if err == nil {
		t.Fatal("Expected error for oversized frame, got nil")
	}
	expected := "exceeds maximum of 1048576 bytes"
	if !bytes.Contains([]byte(err.Error()), []byte(expected)) {
		t.Errorf("Expected error to contain %q, got %v", expected, err)
	}
}

func TestParser_NextFrame_InvalidEnd(t *testing.T) {
	// Frame with invalid end byte
	data := []byte{
		0x01,                   // Type
		0x00, 0x01,             // Channel
		0x00, 0x00, 0x00, 0x01, // Size
		0xCC,                   // Payload
		0xFF,                   // Invalid End (Expected 0xCE)
	}

	parser := NewParser(bytes.NewReader(data))
	_, err := parser.NextFrame()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("invalid frame end byte")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParser_NextFrame_EOF(t *testing.T) {
	parser := NewParser(bytes.NewReader([]byte{0x01, 0x00}))
	_, err := parser.NextFrame()
	if err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Errorf("Expected EOF error, got %v", err)
	}
}
