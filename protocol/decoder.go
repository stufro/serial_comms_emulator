package protocol

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	// defaultInitialBufCap is the initial allocation capacity for the stream decoder buffer.
	defaultInitialBufCap = 1024
)

// ProgressCallback is invoked as raw bytes arrive before a frame is fully assembled.
// bufferCopy contains a safe, cloned snapshot of the current receiver buffer.
type ProgressCallback func(bufferCopy []byte, stage string)

// Decoder provides a robust sliding-window stream parser to decode frames from an io.Reader.
type Decoder struct {
	reader     io.Reader
	buf        []byte
	tmp        [512]byte
	onProgress ProgressCallback
}

// NewDecoder creates a new stream decoder from any io.Reader.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{
		reader: r,
		buf:    make([]byte, 0, defaultInitialBufCap),
	}
}

// SetOnProgress attaches an optional callback for observing in-flight buffer assembly.
func (d *Decoder) SetOnProgress(cb ProgressCallback) {
	d.onProgress = cb
}

func (d *Decoder) notify(stage string) {
	if d.onProgress != nil {
		// Pass a safe clone so concurrent readers in callback don't race
		bufCopy := make([]byte, len(d.buf))
		copy(bufCopy, d.buf)
		d.onProgress(bufCopy, stage)
	}
}

// NextFrame reads from the underlying stream until a valid frame is parsed,
// or returns an error (e.g. io.EOF).
// If corrupted data or false sync words are encountered, the parser slides forward
// and automatically resynchronizes.
func (d *Decoder) NextFrame() (*Frame, error) {
	for {
		// Look for sync word in existing buffer
		syncIdx := -1
		for i := 0; i+1 < len(d.buf); i++ {
			if d.buf[i] == Sync1 && d.buf[i+1] == Sync2 {
				syncIdx = i
				break
			}
		}

		if syncIdx == -1 {
			// No sync word found yet. Keep last byte if it's Sync1 (in case Sync2 is next read)
			if len(d.buf) > 0 && d.buf[len(d.buf)-1] == Sync1 {
				d.buf[0] = Sync1
				d.buf = d.buf[:1]
			} else {
				d.buf = d.buf[:0]
			}

			// Read more bytes
			n, err := d.reader.Read(d.tmp[:])
			if n > 0 {
				d.buf = append(d.buf, d.tmp[:n]...)
				d.notify("Scanning stream for Sync (0xAA 0x55)...")
			}
			if err != nil {
				return nil, err
			}
			continue
		}

		// Discard any garbage bytes prior to sync word
		if syncIdx > 0 {
			copy(d.buf, d.buf[syncIdx:])
			d.buf = d.buf[:len(d.buf)-syncIdx]
		}

		// Check if we have at least the header (8 bytes)
		if len(d.buf) < HeaderSize {
			d.notify("Sync acquired. Reading frame header (Seq + Len)...")
			n, err := d.reader.Read(d.tmp[:])
			if n > 0 {
				d.buf = append(d.buf, d.tmp[:n]...)
			}
			if err != nil {
				return nil, err
			}
			continue
		}

		seq := binary.BigEndian.Uint32(d.buf[2:6])
		payloadLen := int(binary.BigEndian.Uint16(d.buf[6:8]))
		totalFrameLen := MinFrameSize + payloadLen

		// Check if full frame is present in buffer
		if len(d.buf) < totalFrameLen {
			d.notify(fmt.Sprintf("Receiving Frame #%d (%d/%d bytes in buffer)...", seq, len(d.buf), totalFrameLen))
			n, err := d.reader.Read(d.tmp[:])
			if n > 0 {
				d.buf = append(d.buf, d.tmp[:n]...)
			}
			if err != nil {
				return nil, err
			}
			continue
		}

		// We have the complete candidate frame in d.buf[0:totalFrameLen]
		d.notify(fmt.Sprintf("Verifying CRC32 for Frame #%d (%d bytes)...", seq, totalFrameLen))

		headerAndPayload := d.buf[2 : 8+payloadLen]
		expectedCRC := crc32.ChecksumIEEE(headerAndPayload)
		actualCRC := binary.BigEndian.Uint32(d.buf[8+payloadLen : totalFrameLen])

		if expectedCRC != actualCRC {
			// CRC failed. Advance 1 byte past Sync1 to continue searching
			copy(d.buf, d.buf[1:])
			d.buf = d.buf[:len(d.buf)-1]
			continue
		}

		// Valid frame found!
		payloadCopy := make([]byte, payloadLen)
		if payloadLen > 0 {
			copy(payloadCopy, d.buf[8:8+payloadLen])
		}

		frame := &Frame{
			Seq:     seq,
			Payload: payloadCopy,
			CRC:     actualCRC,
		}

		// Advance buffer past this frame
		remaining := len(d.buf) - totalFrameLen
		if remaining > 0 {
			copy(d.buf, d.buf[totalFrameLen:])
			d.buf = d.buf[:remaining]
		} else {
			d.buf = d.buf[:0]
		}

		return frame, nil
	}
}
