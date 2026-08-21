package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"go.bug.st/serial"
)

// OpenPort opens a serial port or pseudo-terminal device.
// It first attempts to open using go.bug.st/serial (with baud rate & 8N1 settings).
// If termios configuration fails (common for some virtual PTYs/pipes), it falls back to standard os.OpenFile.
func OpenPort(portName string, baudRate int) (io.ReadWriteCloser, error) {
	mode := &serial.Mode{
		BaudRate: baudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	p, err := serial.Open(portName, mode)
	if err == nil {
		return p, nil
	}

	// Fallback to standard os file open for virtual PTYs or direct device files
	f, fileErr := os.OpenFile(portName, os.O_RDWR, 0666)
	if fileErr == nil {
		return f, nil
	}

	return nil, fmt.Errorf("failed to open port %q: serial err=(%w), file err=(%w)", portName, err, fileErr)
}

// ByteSentCallback is invoked after each byte is transmitted by TrickleWriter.
type ByteSentCallback func(index int, total int, b byte)

// TrickleWriter wraps an io.Writer to write bytes sequentially with an interval between each byte,
// simulating low-bandwidth serial serialization or radio transmission delay.
type TrickleWriter struct {
	writer     io.Writer
	byteDelay  time.Duration
	onByteSent ByteSentCallback
}

// NewTrickleWriter creates a TrickleWriter with the given delay and optional per-byte callback.
func NewTrickleWriter(w io.Writer, byteDelay time.Duration, onByteSent ByteSentCallback) *TrickleWriter {
	return &TrickleWriter{
		writer:     w,
		byteDelay:  byteDelay,
		onByteSent: onByteSent,
	}
}

// SetByteDelay dynamically updates the inter-byte delay.
func (tw *TrickleWriter) SetByteDelay(d time.Duration) {
	tw.byteDelay = d
}

// Write writes data to the underlying writer byte-by-byte with the configured delay.
func (tw *TrickleWriter) Write(p []byte) (int, error) {
	return tw.WriteContext(context.Background(), p)
}

// WriteContext writes data byte-by-byte, respecting context cancellation for immediate abort.
func (tw *TrickleWriter) WriteContext(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if tw.byteDelay <= 0 {
		n, err := tw.writer.Write(p)
		if tw.onByteSent != nil {
			for i, b := range p[:n] {
				tw.onByteSent(i, len(p), b)
			}
		}
		return n, err
	}

	totalSent := 0
	var single [1]byte

	for i, b := range p {
		select {
		case <-ctx.Done():
			return totalSent, ctx.Err()
		default:
		}

		single[0] = b
		n, err := tw.writer.Write(single[:])
		if err != nil {
			return totalSent, err
		}
		totalSent += n

		if tw.onByteSent != nil {
			tw.onByteSent(i, len(p), b)
		}

		if i+1 < len(p) {
			select {
			case <-ctx.Done():
				return totalSent, ctx.Err()
			case <-time.After(tw.byteDelay):
			}
		}
	}

	return totalSent, nil
}
