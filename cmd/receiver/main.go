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
	"github.com/stufro/serial-protocol/protocol"
	"github.com/stufro/serial-protocol/transport"
)

type receiverConfig struct {
	portPath string
	baudRate int
	station  string
}

func parseFlags(args []string) (*receiverConfig, error) {
	fs := flag.NewFlagSet("receiver", flag.ContinueOnError)
	cfg := &receiverConfig{}

	fs.StringVar(&cfg.portPath, "port", "/tmp/ttyV1", "Serial device or PTY path")
	fs.IntVar(&cfg.baudRate, "baud", 115200, "Baud rate")
	fs.StringVar(&cfg.station, "station", "JPL MISSION CONTROL (PASADENA, CA)", "Receiver station identifier")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func printReceiverHeader(cfg *receiverConfig) {
	terminal.PrintBanner(
		"Pathfinder",
		"DEEP SPACE NETWORK TELEMETRY RECEIVER",
		cfg.station,
		cfg.portPath,
		[2]string{"STATUS", fmt.Sprintf("%sLISTENING FOR SYNC (0xAA 0x55)...%s", terminal.Green, terminal.Reset)},
	)
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

	printReceiverHeader(cfg)

	decoder := protocol.NewDecoder(port)
	var mu sync.Mutex

	// In-flight progress callback to render sliding 32-byte window on a single line
	decoder.SetOnProgress(func(rawBuffer []byte, stage string) {
		mu.Lock()
		defer mu.Unlock()

		displayBytes := rawBuffer
		prefix := ""
		if len(rawBuffer) > 32 {
			displayBytes = rawBuffer[len(rawBuffer)-32:]
			prefix = "... "
		}
		hexPreview := prefix + fmt.Sprintf("% X", displayBytes)

		fmt.Fprintf(out, "%s%s⏳ [INCOMING STREAM] %s | Raw: [%s]%s",
			terminal.ClearLine, terminal.Gray, stage, hexPreview, terminal.Reset)
	})

	frameChan := make(chan *protocol.Frame)
	errChan := make(chan error)

	go func() {
		for {
			frame, err := decoder.NextFrame()
			if err != nil {
				errChan <- err
				if errors.Is(err, io.EOF) {
					return
				}
				continue
			}
			frameChan <- frame
		}
	}()

	totalFrames := 0

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			fmt.Fprint(out, terminal.ClearLine)
			fmt.Fprintf(out, "\n%s[RECEIVER OFFLINE] Total messages decoded: %d%s\n",
				terminal.Amber, totalFrames, terminal.Reset)
			mu.Unlock()
			return nil

		case frame := <-frameChan:
			mu.Lock()
			totalFrames++

			// Clear temporary light grey progress line
			fmt.Fprint(out, terminal.ClearLine)

			// Print finalized transmission banner
			fmt.Fprintf(out, "%s▼ [INCOMING TRANSMISSION]%s Frame #%03d | CRC: 0x%08X %s[OK]%s | Len: %d B\n",
				terminal.Green, terminal.Reset, frame.Seq, frame.CRC, terminal.Green, terminal.Reset, len(frame.Payload))
			fmt.Fprintf(out, "  %s%s%s%s\n\n", terminal.Bold, terminal.Amber, string(frame.Payload), terminal.Reset)
			mu.Unlock()

		case err := <-errChan:
			mu.Lock()
			fmt.Fprint(out, terminal.ClearLine)
			if errors.Is(err, io.EOF) {
				fmt.Fprintf(out, "\n%s[COMMS LOST] Stream closed (EOF).%s\n", terminal.Red, terminal.Reset)
				mu.Unlock()
				return nil
			}
			fmt.Fprintf(out, "%s[PARSER WARNING] %v%s\n", terminal.Red, err, terminal.Reset)
			mu.Unlock()
		}
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
