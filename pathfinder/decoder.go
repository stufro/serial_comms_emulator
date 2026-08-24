package pathfinder

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
	buffer     []byte
	readChunk  [512]byte
	onProgress ProgressCallback
}

// NewDecoder creates a new stream decoder from any io.Reader.
func NewDecoder(reader io.Reader) *Decoder {
	return &Decoder{
		reader: reader,
		buffer: make([]byte, 0, defaultInitialBufCap),
	}
}

// SetOnProgress attaches an optional callback for observing in-flight buffer assembly.
func (decoder *Decoder) SetOnProgress(callback ProgressCallback) {
	decoder.onProgress = callback
}

func (decoder *Decoder) notify(stage string) {
	if decoder.onProgress != nil {
		// Pass a safe clone so concurrent readers in callback don't race
		bufferCopy := make([]byte, len(decoder.buffer))
		copy(bufferCopy, decoder.buffer)
		decoder.onProgress(bufferCopy, stage)
	}
}

// NextFrame reads from the underlying stream until a valid frame is parsed,
// or returns an error (e.g. io.EOF).
// If corrupted data or false sync words are encountered, the parser slides forward
// and automatically resynchronizes.
func (decoder *Decoder) NextFrame() (*Frame, error) {
	for {
		if frame := decoder.extractFrame(); frame != nil {
			return frame, nil
		}

		n, err := decoder.reader.Read(decoder.readChunk[:])
		if n > 0 {
			// Buffer the new bytes and re-parse before surfacing any error:
			// a Reader may return n > 0 alongside io.EOF, and those final
			// bytes can complete a frame.
			decoder.buffer = append(decoder.buffer, decoder.readChunk[:n]...)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

// extractFrame attempts to parse one complete frame out of the buffered data,
// discarding garbage and resynchronizing on corruption as it goes.
// It returns nil when more bytes are required.
func (decoder *Decoder) extractFrame() *Frame {
	for {
		syncIdx := bytes.Index(decoder.buffer, SyncWord[:])
		if syncIdx == -1 {
			if len(decoder.buffer) > 0 {
				decoder.notify("Scanning stream for Sync (0xAA 0x55)...")
			}
			// Discard scanned garbage, but keep a trailing first sync byte
			// in case its partner arrives in the next read.
			if len(decoder.buffer) > 0 && decoder.buffer[len(decoder.buffer)-1] == SyncWord[0] {
				decoder.buffer[0] = SyncWord[0]
				decoder.buffer = decoder.buffer[:1]
			} else {
				decoder.buffer = decoder.buffer[:0]
			}
			return nil
		}

		// Discard any garbage bytes prior to sync word
		if syncIdx > 0 {
			decoder.buffer = decoder.buffer[:copy(decoder.buffer, decoder.buffer[syncIdx:])]
		}

		if len(decoder.buffer) < HeaderSize {
			decoder.notify("Sync acquired. Reading frame header (Seq + Len)...")
			return nil
		}

		seq := binary.BigEndian.Uint32(decoder.buffer[seqOffset:lenOffset])
		payloadLen := int(binary.BigEndian.Uint16(decoder.buffer[lenOffset:payloadOffset]))
		totalFrameLen := MinFrameSize + payloadLen

		if len(decoder.buffer) < totalFrameLen {
			decoder.notify(fmt.Sprintf("Receiving Frame #%d (%d/%d bytes in buffer)...", seq, len(decoder.buffer), totalFrameLen))
			return nil
		}

		decoder.notify(fmt.Sprintf("Verifying CRC32 for Frame #%d (%d bytes)...", seq, totalFrameLen))

		var frame Frame
		if err := frame.UnmarshalBinary(decoder.buffer[:totalFrameLen]); err != nil {
			// Corrupt frame (CRC mismatch): the sync word was a false start.
			// Slide 1 byte past it and resume searching.
			decoder.buffer = decoder.buffer[:copy(decoder.buffer, decoder.buffer[1:])]
			continue
		}

		// Valid frame: advance the buffer past it.
		decoder.buffer = decoder.buffer[:copy(decoder.buffer, decoder.buffer[totalFrameLen:])]
		return &frame
	}
}
