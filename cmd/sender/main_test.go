package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

	assertNoDuplicatePrompts(t, out.String())
}

func TestRunQueuesSecondMessageWhileFirstIsStillTrickling(t *testing.T) {
	// Regression test: a second line typed (and Enter pressed) while the
	// first message is still trickling out used to block the whole input
	// loop until the first send finished, making the terminal appear to
	// hang. sendChan must be buffered so the input loop can keep accepting
	// lines while runSender works through the queue.
	portPath := newFakePort(t)

	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer pipeReader.Close()

	var out bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"-port", portPath, "-byte-delay", "5ms"}, pipeReader, &out)
	}()

	fmt.Fprintln(pipeWriter, "FIRST MESSAGE")
	fmt.Fprintln(pipeWriter, "SECOND MESSAGE")

	// Keep stdin open (not EOF) long enough for both background sends to
	// finish and their resultChan events to be handled by the still-running
	// input loop. Each encoded frame here is roughly 60 bytes (payload plus
	// operator/sol/timestamp metadata and frame overhead), so at 5ms/byte
	// each trickle takes ~300ms and the two together take ~600ms run
	// sequentially by runSender. EOF is what actually exits run(), so
	// closing pipeWriter too early would let drainPendingSends silently
	// absorb the results after run() has already started exiting, hiding
	// the bug this test targets - which is exactly what a prior, shorter
	// sleep in this test accidentally did.
	time.Sleep(800 * time.Millisecond)
	pipeWriter.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run hung instead of queuing the second message")
	}

	wireBytes, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("failed to read fake port contents: %v", err)
	}

	frame1, err := pathfinder.DecodeSingle(wireBytes)
	if err != nil {
		t.Fatalf("expected first frame on the wire, got decode error: %v", err)
	}
	if !strings.Contains(string(frame1.Payload), "FIRST MESSAGE") {
		t.Errorf("expected first frame payload to contain FIRST MESSAGE, got: %s", frame1.Payload)
	}

	frame2, err := pathfinder.DecodeSingle(wireBytes[frame1.WireLen():])
	if err != nil {
		t.Fatalf("expected second frame on the wire, got decode error: %v", err)
	}
	if !strings.Contains(string(frame2.Payload), "SECOND MESSAGE") {
		t.Errorf("expected second frame payload to contain SECOND MESSAGE, got: %s", frame2.Payload)
	}

	assertNoDuplicatePrompts(t, out.String())
}

// ansiEscape matches ANSI/VT100 escape sequences (e.g. "\x1b[1;32m") so
// terminal output can be compared and searched as plain text.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// assertNoDuplicatePrompts fails if the sender prompt ("...] > ") is
// followed immediately by another prompt with nothing - not even a newline -
// in between, which is what a real terminal renders as two prompts glued
// onto the same line.
//
// Regression coverage: a background send result completing used to trigger
// an unconditional prompt redraw even though nothing about the prompt (the
// sequence number) had changed, so a successful send produced a visible
// duplicate of the current prompt line stuck onto the previous one.
func assertNoDuplicatePrompts(t *testing.T, output string) {
	t.Helper()
	plain := ansiEscape.ReplaceAllString(output, "")

	if strings.Contains(plain, "] > [") {
		t.Errorf("found two prompts glued onto the same line (no newline between them):\n%s", plain)
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
