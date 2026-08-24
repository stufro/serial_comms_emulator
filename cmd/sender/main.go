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

// sendQueueCapacity bounds how many encoded frames can wait behind the
// in-flight trickle transmission before the input loop blocks.
const sendQueueCapacity = 32

type senderConfig struct {
	portPath  string
	baudRate  int
	operator  string
	sol       int
	byteDelay time.Duration
}

func parseFlags(args []string) (*senderConfig, error) {
	flagSet := flag.NewFlagSet("sender", flag.ContinueOnError)
	cfg := &senderConfig{}

	flagSet.StringVar(&cfg.portPath, "port", "/tmp/ttyV0", "Serial device or PTY path")
	flagSet.IntVar(&cfg.baudRate, "baud", 115200, "Baud rate")
	flagSet.StringVar(&cfg.operator, "operator", "WATNEY (ARES 3 HAB)", "Operator identifier / callsign")
	flagSet.IntVar(&cfg.sol, "sol", 135, "Mission Sol (Martian solar day)")
	flagSet.DurationVar(&cfg.byteDelay, "byte-delay", 35*time.Millisecond, "Serialization delay per byte (e.g. 25ms, 50ms)")

	if err := flagSet.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func printSenderHeader(out io.Writer, cfg *senderConfig) {
	commands := fmt.Sprintf("Type message and press ENTER to transmit.\n             Special: %s/sol <n>%s, %s/delay <dur>%s, %s/help%s, %s/exit%s",
		terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset, terminal.Amber, terminal.Reset)

	terminal.PrintBanner(
		out,
		"ARES III / JPL DEEP SPACE NETWORK",
		cfg.operator,
		cfg.portPath,
		[2]string{"MISSION", "ARES III (ACIDALIA PLANITIA, MARS)"},
		[2]string{"SOL", fmt.Sprintf("%d", cfg.sol)},
		[2]string{"SERIALIZE", fmt.Sprintf("%s/byte", cfg.byteDelay)},
		[2]string{"COMMANDS", commands},
	)
}

// sendJob is one encoded frame queued up for transmission by the writer goroutine.
type sendJob struct {
	seq        uint32
	frameBytes []byte
}

// sendResult reports the outcome of transmitting a sendJob.
type sendResult struct {
	seq uint32
	err error
}

// runSender owns the port for writing: it takes frames off sendChan one at a
// time and trickles each one out in full before starting the next, so
// concurrent sends from the input loop can never interleave their bytes on
// the wire. It reports each outcome on resultChan and exits when sendChan is
// closed or ctx is cancelled, closing resultChan on the way out so callers
// can drain it to know every queued send has been attempted.
func runSender(ctx context.Context, port io.Writer, byteDelay time.Duration, sendChan <-chan sendJob, resultChan chan<- sendResult) {
	defer close(resultChan)
	trickleWriter := transport.NewTrickleWriter(port, byteDelay)

	for {
		select {
		case <-ctx.Done():
			return

		case job, ok := <-sendChan:
			if !ok {
				return
			}
			_, err := trickleWriter.WriteContext(ctx, job.frameBytes)
			resultChan <- sendResult{seq: job.seq, err: err}
		}
	}
}

// printSendResult reports a failed send and returns whether anything was
// printed. Successful sends stay silent.
func printSendResult(out io.Writer, result sendResult) bool {
	if result.err != nil {
		fmt.Fprintf(out, "\n  %s[TX FAILED Seq #%d: %v]%s\n\n", terminal.Red, result.seq, result.err, terminal.Reset)
		return true
	}
	return false
}

// readLines pumps lines from in onto a channel so the main loop can select
// on user input alongside context cancellation and send results. The line
// channel is closed on EOF; a scanner error is delivered separately.
func readLines(in io.Reader) (<-chan string, <-chan error) {
	lineChan := make(chan string)
	errChan := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			lineChan <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			errChan <- err
		}
		close(lineChan)
	}()

	return lineChan, errChan
}

// senderSession holds the interactive input loop's state: the running
// sequence number and the channels connecting the loop to the writer
// goroutine.
type senderSession struct {
	cfg        *senderConfig
	out        io.Writer
	sendChan   chan sendJob
	resultChan chan sendResult
	seq        uint32
}

func newSenderSession(cfg *senderConfig, out io.Writer) *senderSession {
	return &senderSession{
		cfg:        cfg,
		out:        out,
		sendChan:   make(chan sendJob, sendQueueCapacity),
		resultChan: make(chan sendResult),
		seq:        1,
	}
}

func (session *senderSession) printPrompt() {
	fmt.Fprintf(session.out, "%s[%s | SOL %d | SEQ #%03d]%s > ",
		terminal.Green, session.cfg.operator, session.cfg.sol, session.seq, terminal.Reset)
}

// loop drives the interactive session until the user exits, input reaches
// EOF, or the context is cancelled. Each select case decides whether the
// prompt needs redrawing; the redraw itself happens in exactly one place so
// no case can forget it or duplicate it.
func (session *senderSession) loop(ctx context.Context, lineChan <-chan string, scanErrChan <-chan error) error {
	session.printPrompt()

	for {
		redraw := false

		select {
		case <-ctx.Done():
			fmt.Fprintf(session.out, "\n%s[COMMS OFFLINE] Terminal session terminated.%s\n", terminal.Red, terminal.Reset)
			return nil

		case err := <-scanErrChan:
			return fmt.Errorf("stdin error: %w", err)

		case result := <-session.resultChan:
			// A silent success must not redraw the prompt the user is
			// already sitting at; only a failure has anything to report.
			redraw = printSendResult(session.out, result)

		case line, ok := <-lineChan:
			if !ok {
				// EOF on input: let any already-queued sends finish before closing the port.
				session.drainPending()
				fmt.Fprintf(session.out, "\n%s[COMMS OFFLINE] Closing Pathfinder link.%s\n", terminal.Amber, terminal.Reset)
				return nil
			}
			if exit := session.handleLine(ctx, line); exit {
				return nil
			}
			redraw = true
		}

		if redraw {
			session.printPrompt()
		}
	}
}

// handleLine processes one line of user input and reports whether the
// session should end.
func (session *senderSession) handleLine(ctx context.Context, line string) (exit bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	if strings.HasPrefix(line, "/") {
		if handleCommand(line, session.cfg, session.out) {
			session.drainPending()
			return true
		}
		return false
	}

	return session.queueFrame(ctx, line)
}

// queueFrame encodes the message and hands it to the writer goroutine. It
// returns true only if the session should end because the context was
// cancelled while waiting for queue space.
func (session *senderSession) queueFrame(ctx context.Context, message string) (exit bool) {
	frameBytes, err := session.encodeMessage(message)
	if err != nil {
		fmt.Fprintf(session.out, "%s[ERROR] Failed to encode frame: %v%s\n", terminal.Red, err, terminal.Reset)
		return false
	}

	select {
	case session.sendChan <- sendJob{seq: session.seq, frameBytes: frameBytes}:
	case <-ctx.Done():
		return true
	}

	fmt.Fprintln(session.out)
	session.seq++
	return false
}

// encodeMessage wraps the message in operator/sol/timestamp metadata and
// serializes it as a Pathfinder frame.
func (session *senderSession) encodeMessage(message string) ([]byte, error) {
	timestamp := time.Now().Format("15:04:05")
	payload := fmt.Sprintf("[%s | SOL %d | %s] %s", session.cfg.operator, session.cfg.sol, timestamp, message)
	return pathfinder.Encode(session.seq, []byte(payload))
}

// drainPending closes the send queue and waits for every already-queued job
// to finish, reporting any failures. Without this, run() could return and
// close the port while the writer goroutine was still mid-transmission.
func (session *senderSession) drainPending() {
	close(session.sendChan)
	for result := range session.resultChan {
		printSendResult(session.out, result)
	}
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

	lineChan, scanErrChan := readLines(in)

	session := newSenderSession(cfg, out)
	go runSender(ctx, port, cfg.byteDelay, session.sendChan, session.resultChan)

	return session.loop(ctx, lineChan, scanErrChan)
}

func handleCommand(line string, cfg *senderConfig, out io.Writer) (exit bool) {
	parts := strings.Fields(line)
	command := strings.ToLower(parts[0])

	switch command {
	case "/exit", "/quit":
		fmt.Fprintf(out, "%s[COMMS OFFLINE] Closing Pathfinder link.%s\n", terminal.Amber, terminal.Reset)
		return true

	case "/sol":
		handleSolCommand(parts, cfg, out)

	case "/delay":
		handleDelayCommand(parts, cfg, out)

	case "/operator":
		if len(parts) > 1 {
			cfg.operator = strings.Join(parts[1:], " ")
			fmt.Fprintf(out, "%s[SYSTEM] Operator updated to %s.%s\n", terminal.Cyan, cfg.operator, terminal.Reset)
		}

	case "/clear":
		fmt.Fprint(out, "\033[H\033[2J")
		printSenderHeader(out, cfg)

	case "/help":
		printHelp(out)

	default:
		fmt.Fprintf(out, "%s[SYSTEM] Unknown command %s. Type /help for assistance.%s\n", terminal.Red, parts[0], terminal.Reset)
	}

	return false
}

func handleSolCommand(parts []string, cfg *senderConfig, out io.Writer) {
	if len(parts) == 1 {
		fmt.Fprintf(out, "%s[SYSTEM] Current Sol: %d%s\n", terminal.Cyan, cfg.sol, terminal.Reset)
		return
	}

	newSol, err := strconv.Atoi(parts[1])
	if err != nil {
		fmt.Fprintf(out, "%s[SYSTEM] Invalid Sol number.%s\n", terminal.Red, terminal.Reset)
		return
	}
	cfg.sol = newSol
	fmt.Fprintf(out, "%s[SYSTEM] Mission Sol updated to %d.%s\n", terminal.Cyan, cfg.sol, terminal.Reset)
}

func handleDelayCommand(parts []string, cfg *senderConfig, out io.Writer) {
	if len(parts) == 1 {
		fmt.Fprintf(out, "%s[SYSTEM] Current trickle delay: %s/byte%s\n", terminal.Cyan, cfg.byteDelay, terminal.Reset)
		return
	}

	delay, err := time.ParseDuration(parts[1])
	if err != nil {
		fmt.Fprintf(out, "%s[SYSTEM] Invalid duration format (e.g. 25ms, 50ms, 0s).%s\n", terminal.Red, terminal.Reset)
		return
	}
	cfg.byteDelay = delay
	fmt.Fprintf(out, "%s[SYSTEM] Byte trickle delay set to %s.%s\n", terminal.Cyan, cfg.byteDelay, terminal.Reset)
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, terminal.Cyan+"\nAvailable Commands:"+terminal.Reset)
	fmt.Fprintln(out, "  /delay <dur>    - Set byte trickle delay (e.g. /delay 35ms, /delay 0s)")
	fmt.Fprintln(out, "  /sol <n>        - Update current Martian Sol day")
	fmt.Fprintln(out, "  /operator <name>- Change active operator / callsign")
	fmt.Fprintln(out, "  /clear          - Clear terminal screen and re-render header")
	fmt.Fprintln(out, "  /help           - Display this help message")
	fmt.Fprintln(out, "  /exit           - Terminate comms terminal")
	fmt.Fprintln(out)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
