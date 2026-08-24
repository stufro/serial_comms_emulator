package pathfinder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// SyncWord is the 2-byte frame start delimiter (0xAA 0x55).
var SyncWord = [2]byte{0xAA, 0x55}

const (
	// MaxPayloadSize is the maximum payload size per frame: 65,535 bytes,
	// the largest value representable in the 2-byte length field.
	MaxPayloadSize = 65535

	// HeaderSize: SyncWord (2) + Seq (4) + Length (2) = 8 bytes
	HeaderSize = 8

	// MinFrameSize: SyncWord (2) + Seq (4) + Length (2) + CRC32 (4) = 12 bytes
	MinFrameSize = 12

	// CRCSize: 4 bytes (CRC32 IEEE)
	CRCSize = 4
)

// Byte offsets of each field within a serialized frame, shared by the
// marshaling code below and the stream decoder.
const (
	seqOffset     = 2
	lenOffset     = 6
	payloadOffset = 8
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

	frame := &Frame{
		Seq:     seq,
		Payload: payload,
	}
	frame.CRC = frame.computeCRC()
	return frame, nil
}

// computeCRC calculates the IEEE CRC32 checksum over [Seq (4B) + Length (2B) + Payload (NB)].
func (frame *Frame) computeCRC() uint32 {
	hash := crc32.NewIEEE()
	var header [HeaderSize - len(SyncWord)]byte
	binary.BigEndian.PutUint32(header[0:4], frame.Seq)
	binary.BigEndian.PutUint16(header[4:6], uint16(len(frame.Payload)))
	hash.Write(header[:])
	hash.Write(frame.Payload)
	return hash.Sum32()
}

// WireLen returns the total serialized byte length of the frame.
func (frame *Frame) WireLen() int {
	return MinFrameSize + len(frame.Payload)
}

// String returns a human-readable representation of the frame.
func (frame *Frame) String() string {
	return fmt.Sprintf("Frame(Seq=%d, PayloadLen=%d, CRC=0x%08X)", frame.Seq, len(frame.Payload), frame.CRC)
}

// MarshalBinary encodes the frame into standard Pathfinder binary format:
// [0xAA 0x55] [Seq (4B)] [Len (2B)] [Payload (NB)] [CRC32 (4B)]
func (frame *Frame) MarshalBinary() ([]byte, error) {
	if len(frame.Payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}

	wire := make([]byte, frame.WireLen())
	copy(wire[:seqOffset], SyncWord[:])
	binary.BigEndian.PutUint32(wire[seqOffset:lenOffset], frame.Seq)
	binary.BigEndian.PutUint16(wire[lenOffset:payloadOffset], uint16(len(frame.Payload)))
	copy(wire[payloadOffset:], frame.Payload)
	binary.BigEndian.PutUint32(wire[payloadOffset+len(frame.Payload):], frame.computeCRC())
	return wire, nil
}

// UnmarshalBinary decodes raw bytes into the frame, validating sync and CRC32.
func (frame *Frame) UnmarshalBinary(data []byte) error {
	if len(data) < MinFrameSize {
		return ErrFrameTooShort
	}
	if data[0] != SyncWord[0] || data[1] != SyncWord[1] {
		return ErrInvalidSync
	}

	seq := binary.BigEndian.Uint32(data[seqOffset:lenOffset])
	payloadLen := int(binary.BigEndian.Uint16(data[lenOffset:payloadOffset]))
	expectedTotal := MinFrameSize + payloadLen

	if len(data) < expectedTotal {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrFrameTooShort, expectedTotal, len(data))
	}

	payloadEnd := payloadOffset + payloadLen
	computedCRC := crc32.ChecksumIEEE(data[seqOffset:payloadEnd])
	receivedCRC := binary.BigEndian.Uint32(data[payloadEnd:expectedTotal])

	if computedCRC != receivedCRC {
		return fmt.Errorf("%w: expected 0x%08X, got 0x%08X", ErrInvalidChecksum, computedCRC, receivedCRC)
	}

	frame.Seq = seq
	frame.Payload = make([]byte, payloadLen)
	copy(frame.Payload, data[payloadOffset:payloadEnd])
	frame.CRC = receivedCRC
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
	var frame Frame
	if err := frame.UnmarshalBinary(data); err != nil {
		return nil, err
	}
	return &frame, nil
}
