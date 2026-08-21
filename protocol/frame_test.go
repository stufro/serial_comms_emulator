package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeAndDecodeSingleFrame(t *testing.T) {
	seq := uint32(42)
	payload := []byte("ARES 3 TELEMETRY: ALL SYSTEMS NOMINAL")

	encoded, err := Encode(seq, payload)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	frame, err := DecodeSingle(encoded)
	if err != nil {
		t.Fatalf("DecodeSingle failed: %v", err)
	}

	if frame.Seq != seq {
		t.Errorf("Expected seq %d, got %d", seq, frame.Seq)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Errorf("Expected payload %s, got %s", payload, frame.Payload)
	}
	if frame.WireLen() != len(encoded) {
		t.Errorf("Expected wire len %d, got %d", len(encoded), frame.WireLen())
	}
}

func TestBinaryMarshalerAndUnmarshaler(t *testing.T) {
	frame, err := NewFrame(7, []byte("JPL GROUND CONTROL"))
	if err != nil {
		t.Fatalf("NewFrame failed: %v", err)
	}

	marshaled, err := frame.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	var decoded Frame
	if err := decoded.UnmarshalBinary(marshaled); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if decoded.Seq != frame.Seq || !bytes.Equal(decoded.Payload, frame.Payload) || decoded.CRC != frame.CRC {
		t.Fatalf("Decoded frame does not match original: %+v vs %+v", decoded, frame)
	}

	// Corrupt binary unmarshaling test
	marshaled[len(marshaled)-1] ^= 0xFF
	if err := decoded.UnmarshalBinary(marshaled); !errors.Is(err, ErrInvalidChecksum) {
		t.Fatalf("Expected ErrInvalidChecksum on corrupted frame, got: %v", err)
	}

	// Short binary test
	if err := decoded.UnmarshalBinary([]byte{0xAA, 0x55}); !errors.Is(err, ErrFrameTooShort) {
		t.Fatalf("Expected ErrFrameTooShort, got: %v", err)
	}
}

func TestEmptyPayload(t *testing.T) {
	encoded, err := Encode(10, []byte{})
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	frame, err := DecodeSingle(encoded)
	if err != nil {
		t.Fatalf("DecodeSingle failed: %v", err)
	}

	if frame.Seq != 10 {
		t.Errorf("Expected seq 10, got %d", frame.Seq)
	}
	if len(frame.Payload) != 0 {
		t.Errorf("Expected empty payload, got %v", frame.Payload)
	}
}
