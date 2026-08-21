package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	// Sync1 and Sync2 define the 2-byte frame start delimiter (0xAA 0x55).
	Sync1 byte = 0xAA
	Sync2 byte = 0x55

	// MaxPayloadSize is the maximum allowed payload size per frame (64KB).
	MaxPayloadSize = 65535

	// HeaderSize: SyncWord (2) + Seq (4) + Length (2) = 8 bytes
	HeaderSize = 8

	// MinFrameSize: SyncWord (2) + Seq (4) + Length (2) + CRC32 (4) = 12 bytes
	MinFrameSize = 12

	// CRCSize: 4 bytes (CRC32)
	CRCSize = 4
)

var (
	// ErrPayloadTooLarge is returned when payload exceeds MaxPayloadSize.
	ErrPayloadTooLarge = errors.New("payload exceeds maximum allowed size")
	// ErrInvalidChecksum is returned when frame CRC32 does not match.
	ErrInvalidChecksum = errors.New("invalid checksum (CRC32 mismatch)")
)

// Frame represents a decoded Pathfinder protocol frame.
type Frame struct {
	Seq     uint32
	Payload []byte
	CRC     uint32
}

// Encode builds a wire-format frame byte slice:
// [0xAA, 0x55] [Seq uint32 BE] [Len uint16 BE] [Payload N bytes] [CRC32 uint32 BE]
// CRC32 is calculated over: [Seq (4 bytes) + Len (2 bytes) + Payload (N bytes)]
func Encode(seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}

	payloadLen := uint16(len(payload))
	frameLen := 2 + 4 + 2 + len(payload) + 4
	buf := make([]byte, frameLen)

	// Sync Word
	buf[0] = Sync1
	buf[1] = Sync2

	// Sequence ID (Big Endian)
	binary.BigEndian.PutUint32(buf[2:6], seq)

	// Payload Length (Big Endian)
	binary.BigEndian.PutUint16(buf[6:8], payloadLen)

	// Payload
	copy(buf[8:8+len(payload)], payload)

	// Calculate CRC32 over Seq + Length + Payload
	checksum := crc32.ChecksumIEEE(buf[2 : 8+len(payload)])

	// CRC32 Checksum (Big Endian)
	binary.BigEndian.PutUint32(buf[8+len(payload):], checksum)

	return buf, nil
}

// ProgressCallback is invoked as raw bytes arrive before a frame is fully assembled.
type ProgressCallback func(rawBuffer []byte, stage string)

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
		buf:    make([]byte, 0, 1024),
	}
}

// SetOnProgress attaches a callback for observing in-flight buffer assembly.
func (d *Decoder) SetOnProgress(cb ProgressCallback) {
	d.onProgress = cb
}

func (d *Decoder) notify(stage string) {
	if d.onProgress != nil {
		d.onProgress(d.buf, stage)
	}
}

// NextFrame reads from the underlying stream until a valid frame is parsed,
// or returns an error (e.g. io.EOF).
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
			// No sync word found yet. Keep last byte if it's Sync1
			if len(d.buf) > 0 && d.buf[len(d.buf)-1] == Sync1 {
				d.buf = d.buf[len(d.buf)-1:]
			} else {
				d.buf = d.buf[:0]
			}

			// Read more bytes (reads 1 byte at a time or available chunk)
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
			d.buf = d.buf[syncIdx:]
			syncIdx = 0
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
		totalFrameLen := 2 + 4 + 2 + payloadLen + 4

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
			// CRC failed. Advance 1 byte past Sync1 and continue searching.
			d.buf = d.buf[1:]
			continue
		}

		// Valid frame found!
		payloadCopy := make([]byte, payloadLen)
		copy(payloadCopy, d.buf[8:8+payloadLen])

		frame := &Frame{
			Seq:     seq,
			Payload: payloadCopy,
			CRC:     actualCRC,
		}

		// Advance buffer past this frame
		d.buf = d.buf[totalFrameLen:]

		return frame, nil
	}
}

// DecodeSingle attempts to decode a single frame from a byte slice.
func DecodeSingle(data []byte) (*Frame, error) {
	dec := NewDecoder(bytes.NewReader(data))
	frame, err := dec.NextFrame()
	if err != nil {
		return nil, err
	}
	return frame, nil
}
