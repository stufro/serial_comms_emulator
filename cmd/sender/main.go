package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stufro/serial-protocol/internal/terminal"
	"github.com/stufro/serial-protocol/internal/transport"
	"github.com/stufro/serial-protocol/pathfinder"
)

type senderConfig struct {
	portPath  string
	baudRate  int
	operator  string
	sol       int
	byteDelay time.Duration
}

func parseFlags(args []string) (*senderConfig, error) {
	fs := flag.NewFlagSet("sender", flag.ContinueOnError)
	cfg := &senderConfig{}

	fs.StringVar(&cfg.portPath, "port", "/tmp/ttyV0", "Serial device or PTY path")
	fs.IntVar(&cfg.baudRate, "baud", 115200, "Baud rate")
	fs.StringVar(&cfg.operator, "operator", "WATNEY (ARES 3 HAB)", "Operator identifier / callsign")
	fs.IntVar(&cfg.sol, "sol", 135, "Mission Sol (Martian solar day)")
	fs.DurationVar(&cfg.byteDelay, "byte-delay", 35*time.Millisecond, "Serialization delay per byte (e.g. 25ms, 50ms)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func printSenderHeader(out io.Writer, cfg *senderConfig) {
	legend := fmt.Sprintf("%s[SYNC]%s %s[SEQ]%s %s[LEN]%s %s[PAYLOAD]%s %s[CRC32]%s",
		terminal.Cyan, terminal.Reset,
		terminal.Amber, terminal.Reset,
		terminal.Magenta, terminal.Reset,
		terminal.Green, terminal.Reset,
		terminal.Orange, terminal.Reset)

	commands := fmt.Sprintf("Type message and press ENTER to transmit.\n             Special: %s/sol <n>%s, %s/delay <dur>%s, %s/help%s, %s/exit%s",
		terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset)

	terminal.PrintBanner(
		out,
		"Pathfinder",
		"ARES III / JPL DEEP SPACE NETWORK",
		cfg.operator,
		cfg.portPath,
		[2]string{"MISSION", "ARES III (ACIDALIA PLANITIA, MARS)"},
		[2]string{"SOL", fmt.Sprintf("%d", cfg.sol)},
		[2]string{"SERIALIZE", fmt.Sprintf("%s/byte live hex stream", cfg.byteDelay)},
		[2]string{"LEGEND", legend},
		[2]string{"COMMANDS", commands},
	)
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	port, err := transport.OpenPort(cfg.portPath, cfg.baudRate)
	if err != nil {
		return fmt.Errorf("failed to open port %s: %w", cfg.portPath, err)
	}
	defer port.Close()

	printSenderHeader(out, cfg)

	// Channel for user input lines to allow clean select on context cancellation
	lineChan := make(chan string)
	scanErrChan := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			lineChan <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			scanErrChan <- err
		}
		close(lineChan)
	}()

	var seq uint32 = 1

	for {
		// Render prompt
		fmt.Fprintf(out, "%s[%s | SOL %d | SEQ #%03d]%s > ",
			terminal.Green, cfg.operator, cfg.sol, seq, terminal.Reset)

		select {
		case <-ctx.Done():
			fmt.Fprintf(out, "\n%s[COMMS OFFLINE] Terminal session terminated.%s\n", terminal.Red, terminal.Reset)
			return nil

		case err := <-scanErrChan:
			return fmt.Errorf("stdin error: %w", err)

		case line, ok := <-lineChan:
			if !ok {
				// EOF on input
				fmt.Fprintf(out, "\n%s[COMMS OFFLINE] Closing Pathfinder link.%s\n", terminal.Amber, terminal.Reset)
				return nil
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Handle in-terminal commands
			if strings.HasPrefix(line, "/") {
				if handleCommand(line, cfg, out) {
					return nil // User requested exit
				}
				continue
			}

			// Encode frame with Martian metadata
			timestamp := time.Now().Format("15:04:05")
			payload := fmt.Sprintf("[%s | SOL %d | %s] %s", cfg.operator, cfg.sol, timestamp, line)
			payloadBytes := []byte(payload)

			frame, err := pathfinder.NewFrame(seq, payloadBytes)
			if err != nil {
				fmt.Fprintf(out, "%s[ERROR] Failed to encode frame: %v%s\n", terminal.Red, err, terminal.Reset)
				continue
			}
			frameBytes, err := frame.MarshalBinary()
			if err != nil {
				fmt.Fprintf(out, "%s[ERROR] Failed to encode frame: %v%s\n", terminal.Red, err, terminal.Reset)
				continue
			}

			// Live hex streaming across the wire
			fmt.Fprintf(out, "  %s📡 Wire Stream:%s [", terminal.Amber, terminal.Reset)

			trickleWriter := transport.NewTrickleWriter(port, cfg.byteDelay, func(index, total int, sentByte byte) {
				fmt.Fprint(out, terminal.ColorizeByte(index, len(payloadBytes), sentByte))
				if index+1 < total {
					fmt.Fprint(out, " ")
				}
			})

			bytesWritten, writeErr := trickleWriter.WriteContext(ctx, frameBytes)
			fmt.Fprintln(out, "]")

			if writeErr != nil {
				fmt.Fprintf(out, "  %s[TX FAILED: %v]%s\n\n", terminal.Red, writeErr, terminal.Reset)
				if ctx.Err() != nil {
					return nil
				}
				continue
			}

			fmt.Fprintf(out, "  %s↳ [TX CONFIRMED]%s %d bytes wire | Seq #%d | Checksum: 0x%08X\n\n",
				terminal.Green, terminal.Reset, bytesWritten, seq, frame.CRC)

			seq++
		}
	}
}

func handleCommand(line string, cfg *senderConfig, out io.Writer) (exit bool) {
	parts := strings.Fields(line)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/exit", "/quit":
		fmt.Fprintf(out, "%s[COMMS OFFLINE] Closing Pathfinder link.%s\n", terminal.Amber, terminal.Reset)
		return true

	case "/sol":
		if len(parts) > 1 {
			if newSol, err := strconv.Atoi(parts[1]); err == nil {
				cfg.sol = newSol
				fmt.Fprintf(out, "%s[SYSTEM] Mission Sol updated to %d.%s\n", terminal.Cyan, cfg.sol, terminal.Reset)
			} else {
				fmt.Fprintf(out, "%s[SYSTEM] Invalid Sol number.%s\n", terminal.Red, terminal.Reset)
			}
		} else {
			fmt.Fprintf(out, "%s[SYSTEM] Current Sol: %d%s\n", terminal.Cyan, cfg.sol, terminal.Reset)
		}

	case "/delay":
		if len(parts) > 1 {
			if dur, err := time.ParseDuration(parts[1]); err == nil {
				cfg.byteDelay = dur
				fmt.Fprintf(out, "%s[SYSTEM] Byte trickle delay set to %s.%s\n", terminal.Cyan, cfg.byteDelay, terminal.Reset)
			} else {
				fmt.Fprintf(out, "%s[SYSTEM] Invalid duration format (e.g. 25ms, 50ms, 0s).%s\n", terminal.Red, terminal.Reset)
			}
		} else {
			fmt.Fprintf(out, "%s[SYSTEM] Current trickle delay: %s/byte%s\n", terminal.Cyan, cfg.byteDelay, terminal.Reset)
		}

	case "/operator":
		if len(parts) > 1 {
			cfg.operator = strings.Join(parts[1:], " ")
			fmt.Fprintf(out, "%s[SYSTEM] Operator updated to %s.%s\n", terminal.Cyan, cfg.operator, terminal.Reset)
		}

	case "/clear":
		fmt.Fprint(out, "\033[H\033[2J")
		printSenderHeader(out, cfg)

	case "/help":
		fmt.Fprintln(out, terminal.Cyan+"\nAvailable Commands:"+terminal.Reset)
		fmt.Fprintln(out, "  /delay <dur>    - Set byte trickle delay (e.g. /delay 35ms, /delay 0s)")
		fmt.Fprintln(out, "  /sol <n>        - Update current Martian Sol day")
		fmt.Fprintln(out, "  /operator <name>- Change active operator / callsign")
		fmt.Fprintln(out, "  /clear          - Clear terminal screen and re-render header")
		fmt.Fprintln(out, "  /help           - Display this help message")
		fmt.Fprintln(out, "  /exit           - Terminate comms terminal")
		fmt.Fprintln(out)

	default:
		fmt.Fprintf(out, "%s[SYSTEM] Unknown command %s. Type /help for assistance.%s\n", terminal.Red, parts[0], terminal.Reset)
	}

	return false
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
