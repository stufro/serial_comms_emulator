package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/stufro/serial-protocol/internal/terminal"
	"github.com/stufro/serial-protocol/internal/transport"
	"github.com/stufro/serial-protocol/pathfinder"
)

// previewBytes is how much of the in-flight receive buffer the progress
// line shows before truncating from the left.
const previewBytes = 32

type receiverConfig struct {
	portPath string
	baudRate int
	station  string
}

func parseFlags(args []string) (*receiverConfig, error) {
	flagSet := flag.NewFlagSet("receiver", flag.ContinueOnError)
	cfg := &receiverConfig{}

	flagSet.StringVar(&cfg.portPath, "port", "/tmp/ttyV1", "Serial device or PTY path")
	flagSet.IntVar(&cfg.baudRate, "baud", 115200, "Baud rate")
	flagSet.StringVar(&cfg.station, "station", "JPL MISSION CONTROL (PASADENA, CA)", "Receiver station identifier")

	if err := flagSet.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func printReceiverHeader(out io.Writer, cfg *receiverConfig) {
	terminal.PrintBanner(
		out,
		"DEEP SPACE NETWORK TELEMETRY RECEIVER",
		cfg.station,
		cfg.portPath,
		[2]string{"STATUS", fmt.Sprintf("%sLISTENING FOR SYNC (0xAA 0x55)...%s", terminal.Green, terminal.Reset)},
	)
}

// bufferPreview renders up to the last previewBytes buffered bytes as hex,
// marking left-truncation with an ellipsis.
func bufferPreview(rawBuffer []byte) string {
	if len(rawBuffer) > previewBytes {
		return fmt.Sprintf("... % X", rawBuffer[len(rawBuffer)-previewBytes:])
	}
	return fmt.Sprintf("% X", rawBuffer)
}

// progressRenderer returns a decoder callback that renders the in-flight
// receive buffer as a single self-overwriting status line. outputMu
// serializes it against the frame/shutdown prints in receiveLoop.
func progressRenderer(out io.Writer, outputMu *sync.Mutex) pathfinder.ProgressCallback {
	return func(rawBuffer []byte, stage string) {
		outputMu.Lock()
		defer outputMu.Unlock()

		fmt.Fprintf(out, "%s%s⏳ [INCOMING STREAM] %s | Raw: [%s]%s",
			terminal.ClearLine, terminal.Gray, stage, bufferPreview(rawBuffer), terminal.Reset)
	}
}

// decodeFrames feeds decoded frames onto frameChan until the underlying
// reader fails (including EOF), which it reports on errChan before exiting.
// Frame-level corruption never surfaces here: the decoder resynchronizes
// internally, so any error means the stream itself is done.
func decodeFrames(decoder *pathfinder.Decoder, frameChan chan<- *pathfinder.Frame, errChan chan<- error) {
	for {
		frame, err := decoder.NextFrame()
		if err != nil {
			errChan <- err
			return
		}
		frameChan <- frame
	}
}

// printFrame renders one successfully decoded transmission, replacing the
// transient progress line.
func printFrame(out io.Writer, frame *pathfinder.Frame) {
	fmt.Fprint(out, terminal.ClearLine)
	fmt.Fprintf(out, "%s▼ [INCOMING TRANSMISSION]%s Frame #%03d | CRC: 0x%08X %s[OK]%s | Len: %d B\n",
		terminal.Green, terminal.Reset, frame.Seq, frame.CRC, terminal.Green, terminal.Reset, len(frame.Payload))
	fmt.Fprintf(out, "  %s%s%s%s\n\n", terminal.Bold, terminal.Amber, string(frame.Payload), terminal.Reset)
}

// receiveLoop prints decoded frames as they arrive until the stream ends or
// the context is cancelled.
func receiveLoop(ctx context.Context, out io.Writer, outputMu *sync.Mutex, frameChan <-chan *pathfinder.Frame, errChan <-chan error) error {
	totalFrames := 0

	for {
		select {
		case <-ctx.Done():
			outputMu.Lock()
			fmt.Fprint(out, terminal.ClearLine)
			fmt.Fprintf(out, "\n%s[RECEIVER OFFLINE] Total messages decoded: %d%s\n",
				terminal.Amber, totalFrames, terminal.Reset)
			outputMu.Unlock()
			return nil

		case frame := <-frameChan:
			outputMu.Lock()
			totalFrames++
			printFrame(out, frame)
			outputMu.Unlock()

		case err := <-errChan:
			outputMu.Lock()
			fmt.Fprint(out, terminal.ClearLine)
			outputMu.Unlock()
			if errors.Is(err, io.EOF) {
				fmt.Fprintf(out, "\n%s[COMMS LOST] Stream closed (EOF).%s\n", terminal.Red, terminal.Reset)
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	port, err := transport.OpenPort(cfg.portPath, cfg.baudRate)
	if err != nil {
		return fmt.Errorf("failed to open port %s: %w", cfg.portPath, err)
	}
	defer port.Close()

	printReceiverHeader(out, cfg)

	var outputMu sync.Mutex
	decoder := pathfinder.NewDecoder(port)
	decoder.SetOnProgress(progressRenderer(out, &outputMu))

	frameChan := make(chan *pathfinder.Frame)
	errChan := make(chan error, 1)
	go decodeFrames(decoder, frameChan, errChan)

	return receiveLoop(ctx, out, &outputMu, frameChan, errChan)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
