package pathfinder

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
	frameBytes, _ := Encode(100, []byte("CHUNKED_STREAM_TEST"))

	pipeReader, pipeWriter := io.Pipe()
	go func() {
		defer pipeWriter.Close()
		for _, singleByte := range frameBytes {
			pipeWriter.Write([]byte{singleByte})
		}
	}()

	progressCalls := 0
	decoder := NewDecoder(pipeReader)
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

// eofWithDataReader returns its entire remaining payload together with
// io.EOF on a single Read call, mirroring readers like strings.Reader and
// bytes.Reader that report EOF on the same call as their final bytes
// (see the io.Reader doc comment: "an instance of this general case is
// that a Reader returning a non-zero number of bytes at the end of the
// input stream may return either err == EOF or err == nil").
type eofWithDataReader struct {
	data []byte
	done bool
}

func (r *eofWithDataReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.done = true
	return n, io.EOF
}

func TestNextFrameParsesFinalFrameWhenReadReturnsDataAndEOFTogether(t *testing.T) {
	frameBytes, _ := Encode(7, []byte("LAST_FRAME"))
	decoder := NewDecoder(&eofWithDataReader{data: frameBytes})

	frame, err := decoder.NextFrame()
	if err != nil {
		t.Fatalf("Expected final frame to be parsed despite EOF, got error: %v", err)
	}
	if frame.Seq != 7 || string(frame.Payload) != "LAST_FRAME" {
		t.Fatalf("Unexpected frame: seq=%d, payload=%s", frame.Seq, string(frame.Payload))
	}

	if _, err := decoder.NextFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("Expected EOF on next read, got %v", err)
	}
}
