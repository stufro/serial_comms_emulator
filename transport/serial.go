package transport

import (
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

	return nil, fmt.Errorf("failed to open port %q: serial err=(%v), file err=(%v)", portName, err, fileErr)
}

// TrickleWrite writes data to w byte-by-byte with a delay between each byte,
// simulating low-bandwidth serial transmission or physical radio serialization delay.
// onProgress is an optional callback called after each byte with (bytesSent, totalBytes).
func TrickleWrite(w io.Writer, data []byte, byteDelay time.Duration, onProgress func(sent, total int)) (int, error) {
	if byteDelay <= 0 {
		n, err := w.Write(data)
		if onProgress != nil {
			onProgress(n, len(data))
		}
		return n, err
	}

	totalSent := 0
	buf := []byte{0}

	for i, b := range data {
		buf[0] = b
		n, err := w.Write(buf)
		if err != nil {
			return totalSent, err
		}
		totalSent += n

		if onProgress != nil {
			onProgress(i+1, len(data))
		}

		// Don't sleep after the very last byte
		if i+1 < len(data) {
			time.Sleep(byteDelay)
		}
	}

	return totalSent, nil
}
