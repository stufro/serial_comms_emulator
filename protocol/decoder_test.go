package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestStreamWithGarbageAndMultipleFrames(t *testing.T) {
	var stream bytes.Buffer

	// Leading garbage
	stream.Write([]byte{0x00, 0xFF, 0xAA, 0x12, 0x34, 0xAA})

	// Frame 1
	f1Bytes, _ := Encode(1, []byte("FRAME_ONE"))
	stream.Write(f1Bytes)

	// Interleaved noise + corrupted frame (bad CRC)
	stream.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xAA, 0x55, 0x00, 0x01})
	corruptFrame, _ := Encode(999, []byte("CORRUPTED"))
	corruptFrame[len(corruptFrame)-1] ^= 0xFF
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

func TestFragmentedStreamWithProgressCallback(t *testing.T) {
	fBytes, _ := Encode(100, []byte("CHUNKED_STREAM_TEST"))

	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for _, b := range fBytes {
			w.Write([]byte{b})
		}
	}()

	progressCalls := 0
	decoder := NewDecoder(r)
	decoder.SetOnProgress(func(bufCopy []byte, stage string) {
		progressCalls++
		if len(bufCopy) == 0 {
			t.Error("Expected non-empty buffer snapshot in progress callback")
		}
	})

	frame, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Failed to decode chunked frame: %v", err)
	}
	if frame.Seq != 100 || string(frame.Payload) != "CHUNKED_STREAM_TEST" {
		t.Fatalf("Unexpected decoded frame: %+v", frame)
	}
	if progressCalls == 0 {
		t.Error("Expected at least one progress callback invocation")
	}
}
