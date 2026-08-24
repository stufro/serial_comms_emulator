package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stufro/serial-protocol/pathfinder"
)

// newFakePort writes the given bytes to a temp file and returns its path.
// OpenPort's os.OpenFile fallback opens plain files as an io.ReadWriteCloser,
// so this stands in for a serial device without needing a real one.
func newFakePort(t *testing.T, contents []byte) string {
	t.Helper()
	portPath := filepath.Join(t.TempDir(), "fake-port")
	if err := os.WriteFile(portPath, contents, 0666); err != nil {
		t.Fatalf("failed to create fake port file: %v", err)
	}
	return portPath
}

func TestRunDecodesFramesThenExitsCleanlyOnEOF(t *testing.T) {
	frame1, _ := pathfinder.Encode(1, []byte("HELLO"))
	frame2, _ := pathfinder.Encode(2, []byte("WORLD"))
	portPath := newFakePort(t, append(frame1, frame2...))

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := run(ctx, []string{"-port", portPath}, &out)
	if err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Frame #001") || !strings.Contains(output, "HELLO") {
		t.Errorf("expected output to contain decoded frame 1, got:\n%s", output)
	}
	if !strings.Contains(output, "Frame #002") || !strings.Contains(output, "WORLD") {
		t.Errorf("expected output to contain decoded frame 2, got:\n%s", output)
	}
	if !strings.Contains(output, "COMMS LOST") {
		t.Errorf("expected clean EOF shutdown message, got:\n%s", output)
	}
}

func TestRunStopsOnContextCancellation(t *testing.T) {
	// A FIFO blocks on read until a writer has data (or closes), so run()'s
	// decoder goroutine stays parked mid-read and the only way run() returns
	// is via context cancellation - unlike a plain empty file, which would
	// hit EOF immediately and return before cancellation is even observed.
	portPath := filepath.Join(t.TempDir(), "fake-fifo-port")
	if err := syscall.Mkfifo(portPath, 0666); err != nil {
		t.Skipf("mkfifo not supported on this platform: %v", err)
	}

	// Opening either end of a FIFO blocks until the other end is also
	// opened, so open the writer side in a goroutine while run() (via
	// OpenPort) opens the read side on the main path.
	writerOpened := make(chan *os.File, 1)
	go func() {
		writer, err := os.OpenFile(portPath, os.O_WRONLY, 0)
		if err == nil {
			writerOpened <- writer
		} else {
			writerOpened <- nil
		}
	}()

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-port", portPath}, &out) }()

	writer := <-writerOpened
	if writer == nil {
		t.Fatal("failed to open FIFO writer")
	}
	defer writer.Close()

	// Give run() a moment to reach its blocking read before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after context cancellation")
	}

	if !strings.Contains(out.String(), "RECEIVER OFFLINE") {
		t.Errorf("expected offline shutdown message, got:\n%s", out.String())
	}
}

func TestRunReturnsErrorOnInvalidFlags(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"-unknown-flag"}, &out)
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}
