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

// NewTrickleWriter creates a TrickleWriter with the given delay and optional per-byte callback.
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
		written, err := trickleWriter.writer.Write(data)
		return written, err
	}

	totalSent := 0
	var single [1]byte

	for index, sentByte := range data {
		select {
		case <-ctx.Done():
			return totalSent, ctx.Err()
		default:
		}

		single[0] = sentByte
		written, err := trickleWriter.writer.Write(single[:])
		if err != nil {
			return totalSent, err
		}
		totalSent += written

		if index+1 < len(data) {
			select {
			case <-ctx.Done():
				return totalSent, ctx.Err()
			case <-time.After(trickleWriter.byteDelay):
			}
		}
	}

	return totalSent, nil
}
