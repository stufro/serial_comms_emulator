package transport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenPortFallsBackToFileOpenForNonSerialPath(t *testing.T) {
	// A plain regular file isn't a TTY, so go.bug.st/serial's termios setup
	// fails on it, which is exactly the case OpenPort's os.OpenFile fallback
	// exists for (e.g. a socat-created PTY link can hit the same fallback).
	portPath := filepath.Join(t.TempDir(), "fake-port")
	if err := os.WriteFile(portPath, nil, 0666); err != nil {
		t.Fatalf("failed to create fake port file: %v", err)
	}

	port, err := OpenPort(portPath, 115200)
	if err != nil {
		t.Fatalf("expected fallback file open to succeed, got error: %v", err)
	}
	defer port.Close()

	if _, err := port.Write([]byte("PING")); err != nil {
		t.Errorf("expected to write to fallback-opened port, got error: %v", err)
	}
}

func TestOpenPortReturnsErrorWhenPathDoesNotExist(t *testing.T) {
	portPath := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := OpenPort(portPath, 115200)
	if err == nil {
		t.Fatal("expected error for nonexistent port path, got nil")
	}
}

func TestTrickleWriter(t *testing.T) {
	var destination bytes.Buffer
	data := []byte("HELLO")
	byteDelay := 5 * time.Millisecond

	var sentIndices []int
	onByte := func(index, total int, sentByte byte) {
		sentIndices = append(sentIndices, index)
	}

	trickleWriter := NewTrickleWriter(&destination, byteDelay, onByte)

	start := time.Now()
	written, err := trickleWriter.Write(data)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if written != len(data) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(data), written)
	}
	if destination.String() != "HELLO" {
		t.Errorf("Expected content 'HELLO', got '%s'", destination.String())
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
	var destination bytes.Buffer
	data := []byte("LONG_MESSAGE_TO_BE_CANCELED")
	trickleWriter := NewTrickleWriter(&destination, 50*time.Millisecond, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	written, err := trickleWriter.WriteContext(ctx, data)
	if err == nil {
		t.Fatalf("Expected context cancellation error, got nil")
	}
	if written >= len(data) {
		t.Fatalf("Expected partial write, wrote all %d bytes", written)
	}
}
