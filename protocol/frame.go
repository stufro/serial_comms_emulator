package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
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

	// CRCSize: 4 bytes (CRC32 IEEE)
	CRCSize = 4
)

var (
	// ErrPayloadTooLarge is returned when payload exceeds MaxPayloadSize.
	ErrPayloadTooLarge = errors.New("payload exceeds maximum allowed size")
	// ErrInvalidChecksum is returned when frame CRC32 does not match.
	ErrInvalidChecksum = errors.New("invalid checksum (CRC32 mismatch)")
	// ErrFrameTooShort is returned when unmarshaling data smaller than MinFrameSize.
	ErrFrameTooShort = errors.New("frame binary data too short")
	// ErrInvalidSync is returned when unmarshaling data with an invalid sync word.
	ErrInvalidSync = errors.New("invalid sync word header")
)

// Frame represents a decoded Pathfinder protocol frame.
type Frame struct {
	Seq     uint32
	Payload []byte
	CRC     uint32
}

// NewFrame creates and initializes a Frame with the computed CRC32 checksum.
func NewFrame(seq uint32, payload []byte) (*Frame, error) {
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}

	f := &Frame{
		Seq:     seq,
		Payload: payload,
	}
	f.CRC = f.computeCRC()
	return f, nil
}

// computeCRC calculates the IEEE CRC32 checksum over [Seq (4B) + Length (2B) + Payload (NB)].
func (f *Frame) computeCRC() uint32 {
	h := crc32.NewIEEE()
	var header [6]byte
	binary.BigEndian.PutUint32(header[0:4], f.Seq)
	binary.BigEndian.PutUint16(header[4:6], uint16(len(f.Payload)))
	h.Write(header[:])
	if len(f.Payload) > 0 {
		h.Write(f.Payload)
	}
	return h.Sum32()
}

// WireLen returns the total serialized byte length of the frame.
func (f *Frame) WireLen() int {
	return MinFrameSize + len(f.Payload)
}

// String returns a human-readable representation of the frame.
func (f *Frame) String() string {
	return fmt.Sprintf("Frame(Seq=%d, PayloadLen=%d, CRC=0x%08X)", f.Seq, len(f.Payload), f.CRC)
}

// MarshalBinary encodes the frame into standard Pathfinder binary format:
// [0xAA 0x55] [Seq (4B)] [Len (2B)] [Payload (NB)] [CRC32 (4B)]
func (f *Frame) MarshalBinary() ([]byte, error) {
	if len(f.Payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}

	payloadLen := uint16(len(f.Payload))
	frameLen := MinFrameSize + len(f.Payload)
	buf := make([]byte, frameLen)

	// Sync Word
	buf[0] = Sync1
	buf[1] = Sync2

	// Sequence ID (Big Endian)
	binary.BigEndian.PutUint32(buf[2:6], f.Seq)

	// Payload Length (Big Endian)
	binary.BigEndian.PutUint16(buf[6:8], payloadLen)

	// Payload
	if len(f.Payload) > 0 {
		copy(buf[8:8+len(f.Payload)], f.Payload)
	}

	// Compute & append CRC32
	checksum := crc32.ChecksumIEEE(buf[2 : 8+len(f.Payload)])
	binary.BigEndian.PutUint32(buf[8+len(f.Payload):], checksum)

	return buf, nil
}

// UnmarshalBinary decodes raw bytes into the frame, validating sync and CRC32.
func (f *Frame) UnmarshalBinary(data []byte) error {
	if len(data) < MinFrameSize {
		return ErrFrameTooShort
	}
	if data[0] != Sync1 || data[1] != Sync2 {
		return ErrInvalidSync
	}

	seq := binary.BigEndian.Uint32(data[2:6])
	payloadLen := int(binary.BigEndian.Uint16(data[6:8]))
	expectedTotal := MinFrameSize + payloadLen

	if len(data) < expectedTotal {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrFrameTooShort, expectedTotal, len(data))
	}

	headerAndPayload := data[2 : 8+payloadLen]
	computedCRC := crc32.ChecksumIEEE(headerAndPayload)
	rxCRC := binary.BigEndian.Uint32(data[8+payloadLen : expectedTotal])

	if computedCRC != rxCRC {
		return fmt.Errorf("%w: expected 0x%08X, got 0x%08X", ErrInvalidChecksum, computedCRC, rxCRC)
	}

	payloadCopy := make([]byte, payloadLen)
	if payloadLen > 0 {
		copy(payloadCopy, data[8:8+payloadLen])
	}

	f.Seq = seq
	f.Payload = payloadCopy
	f.CRC = rxCRC
	return nil
}

// Encode is a package-level convenience function to build a serialized frame byte slice.
func Encode(seq uint32, payload []byte) ([]byte, error) {
	frame, err := NewFrame(seq, payload)
	if err != nil {
		return nil, err
	}
	return frame.MarshalBinary()
}

// DecodeSingle attempts to decode a single frame from a byte slice.
func DecodeSingle(data []byte) (*Frame, error) {
	var f Frame
	if err := f.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return &f, nil
}
