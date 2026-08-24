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

	port, serialErr := serial.Open(portName, mode)
	if serialErr == nil {
		return port, nil
	}

	// Fallback to standard os file open for virtual PTYs or direct device files
	file, fileErr := os.OpenFile(portName, os.O_RDWR, 0666)
	if fileErr == nil {
		return file, nil
	}

	return nil, fmt.Errorf("failed to open port %q: serial err=(%w), file err=(%w)", portName, serialErr, fileErr)
}

// TrickleWriter wraps an io.Writer to write bytes sequentially with an interval between each byte,
// simulating low-bandwidth serial serialization or radio transmission delay.
type TrickleWriter struct {
	writer    io.Writer
	byteDelay time.Duration
}

// NewTrickleWriter creates a TrickleWriter with the given inter-byte delay.
// A delay of zero (or less) writes in a single burst.
func NewTrickleWriter(writer io.Writer, byteDelay time.Duration) *TrickleWriter {
	return &TrickleWriter{
		writer:    writer,
		byteDelay: byteDelay,
	}
}

// Write writes data to the underlying writer byte-by-byte with the configured delay.
func (trickleWriter *TrickleWriter) Write(data []byte) (int, error) {
	return trickleWriter.WriteContext(context.Background(), data)
}

// WriteContext writes data byte-by-byte, respecting context cancellation for immediate abort.
func (trickleWriter *TrickleWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if trickleWriter.byteDelay <= 0 {
		return trickleWriter.writer.Write(data)
	}

	totalSent := 0
	for index := range data {
		if err := ctx.Err(); err != nil {
			return totalSent, err
		}

		written, err := trickleWriter.writer.Write(data[index : index+1])
		totalSent += written
		if err != nil {
			return totalSent, err
		}

		if index+1 < len(data) {
			if err := trickleWriter.waitByteDelay(ctx); err != nil {
				return totalSent, err
			}
		}
	}

	return totalSent, nil
}

// waitByteDelay sleeps for one inter-byte interval, cutting the sleep short
// with an error if the context is cancelled first.
func (trickleWriter *TrickleWriter) waitByteDelay(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(trickleWriter.byteDelay):
		return nil
	}
}
