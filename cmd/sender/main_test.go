package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stufro/serial-protocol/pathfinder"
)

// newFakePort creates an empty temp file. OpenPort's os.OpenFile fallback
// opens plain files as an io.ReadWriteCloser, so this stands in for a
// serial device without needing a real one.
func newFakePort(t *testing.T) string {
	t.Helper()
	portPath := filepath.Join(t.TempDir(), "fake-port")
	if err := os.WriteFile(portPath, nil, 0666); err != nil {
		t.Fatalf("failed to create fake port file: %v", err)
	}
	return portPath
}

func TestRunTransmitsTypedLineAsFrameOnWire(t *testing.T) {
	portPath := newFakePort(t)

	in := strings.NewReader("HOUSTON WE HAVE A PROBLEM\n")
	var out bytes.Buffer

	// byte-delay 0 makes the trickle writer write in a single burst,
	// keeping the test fast and deterministic.
	err := run(context.Background(), []string{"-port", portPath, "-byte-delay", "0s"}, in, &out)
	if err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "TX CONFIRMED") {
		t.Fatalf("expected TX CONFIRMED in output, got:\n%s", out.String())
	}

	wireBytes, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("failed to read fake port contents: %v", err)
	}

	frame, err := pathfinder.DecodeSingle(wireBytes)
	if err != nil {
		t.Fatalf("expected a valid frame on the wire, got decode error: %v", err)
	}
	if !strings.Contains(string(frame.Payload), "HOUSTON WE HAVE A PROBLEM") {
		t.Errorf("expected payload to contain typed message, got: %s", frame.Payload)
	}
}

func TestRunPrintsRealChecksumNotCRCSizeConstant(t *testing.T) {
	// Regression test: TX CONFIRMED used to print protocol.CRCSize (a
	// constant, 4) instead of the frame's actual CRC32 checksum.
	portPath := newFakePort(t)
	in := strings.NewReader("PING\n")
	var out bytes.Buffer

	if err := run(context.Background(), []string{"-port", portPath, "-byte-delay", "0s"}, in, &out); err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}

	if strings.Contains(out.String(), "Checksum: 0x00000004") {
		t.Errorf("TX CONFIRMED printed the CRCSize constant instead of the real checksum:\n%s", out.String())
	}

	wireBytes, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("failed to read fake port contents: %v", err)
	}
	frame, err := pathfinder.DecodeSingle(wireBytes)
	if err != nil {
		t.Fatalf("expected a valid frame on the wire, got decode error: %v", err)
	}

	expected := fmt.Sprintf("Checksum: 0x%08X", frame.CRC)
	if !strings.Contains(out.String(), expected) {
		t.Errorf("expected output to contain actual checksum %s, got:\n%s", expected, out.String())
	}
}

func TestRunExitsOnSlashExitCommand(t *testing.T) {
	portPath := newFakePort(t)
	in := strings.NewReader("/exit\n")
	var out bytes.Buffer

	err := run(context.Background(), []string{"-port", portPath}, in, &out)
	if err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "COMMS OFFLINE") {
		t.Errorf("expected COMMS OFFLINE message after /exit, got:\n%s", out.String())
	}
}

func TestRunExitsCleanlyOnStdinEOF(t *testing.T) {
	portPath := newFakePort(t)
	in := strings.NewReader("") // immediate EOF, no lines
	var out bytes.Buffer

	err := run(context.Background(), []string{"-port", portPath}, in, &out)
	if err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Closing Pathfinder link") {
		t.Errorf("expected closing message on stdin EOF, got:\n%s", out.String())
	}
}

func TestRunStopsOnContextCancellation(t *testing.T) {
	portPath := newFakePort(t)

	// A pipe reader that's never written to or closed blocks the scanner
	// goroutine forever, so the only way run() returns is cancellation.
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer pipeReader.Close()
	defer pipeWriter.Close()

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-port", portPath}, pipeReader, &out) }()

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

	if !strings.Contains(out.String(), "COMMS OFFLINE") {
		t.Errorf("expected offline shutdown message, got:\n%s", out.String())
	}
}

func TestRunReturnsErrorOnInvalidFlags(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"-unknown-flag"}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}

func TestRunReturnsErrorWhenPortCannotBeOpened(t *testing.T) {
	portPath := filepath.Join(t.TempDir(), "does-not-exist")
	var out bytes.Buffer
	err := run(context.Background(), []string{"-port", portPath}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected error when port cannot be opened, got nil")
	}
}

func TestHandleCommandUpdatesSolAndOperator(t *testing.T) {
	cfg := &senderConfig{sol: 100, operator: "WATNEY"}
	var out bytes.Buffer

	if exit := handleCommand("/sol 200", cfg, &out); exit {
		t.Fatal("did not expect /sol to signal exit")
	}
	if cfg.sol != 200 {
		t.Errorf("expected sol to be updated to 200, got %d", cfg.sol)
	}

	if exit := handleCommand("/operator JPL UPLINK", cfg, &out); exit {
		t.Fatal("did not expect /operator to signal exit")
	}
	if cfg.operator != "JPL UPLINK" {
		t.Errorf("expected operator to be updated, got %q", cfg.operator)
	}
}

func TestHandleCommandRejectsInvalidSol(t *testing.T) {
	cfg := &senderConfig{sol: 100}
	var out bytes.Buffer

	handleCommand("/sol not-a-number", cfg, &out)

	if cfg.sol != 100 {
		t.Errorf("expected sol to remain unchanged on invalid input, got %d", cfg.sol)
	}
	if !strings.Contains(out.String(), "Invalid Sol number") {
		t.Errorf("expected invalid sol message, got: %s", out.String())
	}
}

func TestHandleCommandUnknownCommandDoesNotExit(t *testing.T) {
	cfg := &senderConfig{}
	var out bytes.Buffer

	if exit := handleCommand("/bogus", cfg, &out); exit {
		t.Error("unknown command should not signal exit")
	}
	if !strings.Contains(out.String(), "Unknown command") {
		t.Errorf("expected unknown command message, got: %s", out.String())
	}
}
