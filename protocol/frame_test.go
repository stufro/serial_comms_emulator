package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestEncodeAndDecodeSingleFrame(t *testing.T) {
	seq := uint32(42)
	payload := []byte("ARES 3 TELEMETRY: ALL SYSTEMS NOMINAL")

	encoded, err := Encode(seq, payload)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoder := NewDecoder(bytes.NewReader(encoded))
	frame, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if frame.Seq != seq {
		t.Errorf("Expected seq %d, got %d", seq, frame.Seq)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Errorf("Expected payload %s, got %s", payload, frame.Payload)
	}
}

func TestStreamWithGarbageAndMultipleFrames(t *testing.T) {
	// Create a stream with leading garbage, interleaved garbage, false sync words, and valid frames
	var stream bytes.Buffer

	// Leading garbage
	stream.Write([]byte{0x00, 0xFF, 0xAA, 0x12, 0x34, 0xAA})

	// Frame 1
	f1Bytes, _ := Encode(1, []byte("FRAME_ONE"))
	stream.Write(f1Bytes)

	// Interleaved noise + corrupted frame (bad CRC)
	stream.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xAA, 0x55, 0x00, 0x01}) // Partial / noise
	corruptFrame, _ := Encode(999, []byte("CORRUPTED"))
	corruptFrame[len(corruptFrame)-1] ^= 0xFF // Flip bits in CRC
	stream.Write(corruptFrame)

	// Frame 2
	f2Bytes, _ := Encode(2, []byte("FRAME_TWO"))
	stream.Write(f2Bytes)

	decoder := NewDecoder(&stream)

	// Read frame 1
	frame1, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Expected frame 1, got error: %v", err)
	}
	if frame1.Seq != 1 || string(frame1.Payload) != "FRAME_ONE" {
		t.Fatalf("Unexpected frame 1: seq=%d, payload=%s", frame1.Seq, string(frame1.Payload))
	}

	// Reading next should skip the corrupted data and successfully find frame 2
	frame2, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Expected frame 2, got error: %v", err)
	}
	if frame2.Seq != 2 || string(frame2.Payload) != "FRAME_TWO" {
		t.Fatalf("Unexpected frame 2: seq=%d, payload=%s", frame2.Seq, string(frame2.Payload))
	}

	// Next read should be EOF
	_, err = decoder.NextFrame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Expected EOF, got %v", err)
	}
}

func TestEmptyPayload(t *testing.T) {
	encoded, err := Encode(10, []byte{})
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoder := NewDecoder(bytes.NewReader(encoded))
	frame, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if frame.Seq != 10 {
		t.Errorf("Expected seq 10, got %d", frame.Seq)
	}
	if len(frame.Payload) != 0 {
		t.Errorf("Expected empty payload, got %v", frame.Payload)
	}
}

func TestFragmentedStream(t *testing.T) {
	// Simulate fragmented serial stream arriving 1 byte at a time
	fBytes, _ := Encode(100, []byte("CHUNKED_STREAM_TEST"))

	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for _, b := range fBytes {
			w.Write([]byte{b})
		}
	}()

	decoder := NewDecoder(r)
	frame, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Failed to decode chunked frame: %v", err)
	}
	if frame.Seq != 100 || string(frame.Payload) != "CHUNKED_STREAM_TEST" {
		t.Fatalf("Unexpected decoded frame: %+v", frame)
	}
}
