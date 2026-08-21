package transport

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestTrickleWriter(t *testing.T) {
	var buf bytes.Buffer
	data := []byte("HELLO")
	byteDelay := 5 * time.Millisecond

	var sentIndices []int
	onByte := func(index, total int, b byte) {
		sentIndices = append(sentIndices, index)
	}

	tw := NewTrickleWriter(&buf, byteDelay, onByte)

	start := time.Now()
	n, err := tw.Write(data)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(data), n)
	}
	if buf.String() != "HELLO" {
		t.Errorf("Expected content 'HELLO', got '%s'", buf.String())
	}
	if len(sentIndices) != len(data) {
		t.Errorf("Expected %d callbacks, got %d", len(data), len(sentIndices))
	}
	// 4 inter-byte sleeps of 5ms = ~20ms minimum
	if elapsed < 15*time.Millisecond {
		t.Errorf("Elapsed time %v was too fast for trickle delay", elapsed)
	}
}

func TestTrickleWriterContextCancellation(t *testing.T) {
	var buf bytes.Buffer
	data := []byte("LONG_MESSAGE_TO_BE_CANCELED")
	tw := NewTrickleWriter(&buf, 50*time.Millisecond, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	n, err := tw.WriteContext(ctx, data)
	if err == nil {
		t.Fatalf("Expected context cancellation error, got nil")
	}
	if n >= len(data) {
		t.Fatalf("Expected partial write, wrote all %d bytes", n)
	}
}
