package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stufro/serial-protocol/protocol"
	"github.com/stufro/serial-protocol/transport"
)

// ANSI color codes for vintage terminal aesthetic
const (
	colorReset   = "\033[0m"
	colorGreen   = "\033[1;32m"
	colorAmber   = "\033[1;33m"
	colorCyan    = "\033[1;36m"
	colorRed     = "\033[1;31m"
	colorMagenta = "\033[1;35m"
	colorDim     = "\033[2m"
	colorBold    = "\033[1m"
	colorOrange  = "\033[38;5;208m"
)

func printMartianBanner(operator string, sol int, port string, byteDelay time.Duration) {
	fmt.Print(colorOrange)
	fmt.Println(`  ____       _   _        __ _           _             `)
	fmt.Println(` |  _ \ __ _| |_| |__   / _(_)_ __   __| | ___ _ __   `)
	fmt.Println(` | |_) / _` + "`" + ` | __| '_ \ | |_| | '_ \ / _` + "`" + ` |/ _ \ '__|  `)
	fmt.Println(` |  __/ (_| | |_| | | ||  _| | | | | (_| |  __/ |     `)
	fmt.Println(` |_|   \__,_|\__|_| |_||_| |_|_| |_|\__,_|\___|_|     `)
	fmt.Println(`             ARES III / JPL DEEP SPACE NETWORK         `)
	fmt.Print(colorReset)
	fmt.Println(colorDim + strings.Repeat("─", 64) + colorReset)
	fmt.Printf("%s[STATION]%s    %s%s%s\n", colorAmber, colorReset, colorBold, operator, colorReset)
	fmt.Printf("%s[MISSION]%s    ARES III (ACIDALIA PLANITIA, MARS)\n", colorAmber, colorReset)
	fmt.Printf("%s[SOL]%s        %d\n", colorAmber, colorReset, sol)
	fmt.Printf("%s[LINK]%s       %s (SERIAL PIPE)\n", colorAmber, colorReset, port)
	fmt.Printf("%s[SERIALIZE]%s  %s/byte live hex stream\n", colorAmber, colorReset, byteDelay)
	fmt.Printf("%s[LEGEND]%s     %s[SYNC]%s %s[SEQ]%s %s[LEN]%s %s[PAYLOAD]%s %s[CRC32]%s\n",
		colorAmber, colorReset,
		colorCyan, colorReset,
		colorAmber, colorReset,
		colorMagenta, colorReset,
		colorGreen, colorReset,
		colorOrange, colorReset)
	fmt.Printf("%s[COMMANDS]%s   Type message and press ENTER to transmit.\n", colorCyan, colorReset)
	fmt.Printf("             Special: %s/sol <n>%s, %s/delay <dur>%s, %s/help%s, %s/exit%s\n",
		colorAmber, colorReset, colorAmber, colorReset, colorAmber, colorReset, colorAmber, colorReset)
	fmt.Println(colorDim + strings.Repeat("─", 64) + colorReset)
	fmt.Println()
}

// getFieldColor returns the syntax highlight color for a byte based on its frame offset
func getFieldColor(idx, payloadLen int) string {
	switch {
	case idx < 2:
		return colorCyan // Sync Word (0xAA 0x55)
	case idx < 6:
		return colorAmber // Sequence ID (4 bytes)
	case idx < 8:
		return colorMagenta // Payload Length (2 bytes)
	case idx < 8+payloadLen:
		return colorGreen // Payload bytes
	default:
		return colorOrange // CRC32 Checksum (4 bytes)
	}
}

func main() {
	portPath := flag.String("port", "/tmp/ttyV0", "Serial device or PTY path")
	baudRate := flag.Int("baud", 115200, "Baud rate")
	operator := flag.String("operator", "WATNEY (ARES 3 HAB)", "Operator identifier / callsign")
	sol := flag.Int("sol", 135, "Mission Sol (Martian solar day)")
	byteDelay := flag.Duration("byte-delay", 35*time.Millisecond, "Serialization delay per byte (e.g. 25ms, 50ms)")
	flag.Parse()

	port, err := transport.OpenPort(*portPath, *baudRate)
	if err != nil {
		log.Fatalf("Failed to open port %s: %v", *portPath, err)
	}
	defer port.Close()

	printMartianBanner(*operator, *sol, *portPath, *byteDelay)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Printf("\n\n%s[COMMS OFFLINE] Terminal session terminated.%s\n", colorRed, colorReset)
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	var seq uint32 = 1

	for {
		// Display Martian terminal prompt
		prompt := fmt.Sprintf("%s[%s | SOL %d | SEQ #%03d]%s > ", colorGreen, *operator, *sol, seq, colorReset)
		fmt.Print(prompt)

		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Handle built-in terminal commands
		if strings.HasPrefix(line, "/") {
			parts := strings.Fields(line)
			cmd := strings.ToLower(parts[0])

			switch cmd {
			case "/exit", "/quit":
				fmt.Printf("%s[COMMS OFFLINE] Closing Pathfinder link.%s\n", colorAmber, colorReset)
				return

			case "/sol":
				if len(parts) > 1 {
					if newSol, err := strconv.Atoi(parts[1]); err == nil {
						*sol = newSol
						fmt.Printf("%s[SYSTEM] Mission Sol updated to %d.%s\n", colorCyan, *sol, colorReset)
					} else {
						fmt.Printf("%s[SYSTEM] Invalid Sol number.%s\n", colorRed, colorReset)
					}
				} else {
					fmt.Printf("%s[SYSTEM] Current Sol: %d%s\n", colorCyan, *sol, colorReset)
				}
				continue

			case "/delay":
				if len(parts) > 1 {
					if dur, err := time.ParseDuration(parts[1]); err == nil {
						*byteDelay = dur
						fmt.Printf("%s[SYSTEM] Byte trickle delay set to %s.%s\n", colorCyan, *byteDelay, colorReset)
					} else {
						fmt.Printf("%s[SYSTEM] Invalid duration format (e.g. 25ms, 50ms, 0s).%s\n", colorRed, colorReset)
					}
				} else {
					fmt.Printf("%s[SYSTEM] Current trickle delay: %s/byte%s\n", colorCyan, *byteDelay, colorReset)
				}
				continue

			case "/operator":
				if len(parts) > 1 {
					*operator = strings.Join(parts[1:], " ")
					fmt.Printf("%s[SYSTEM] Operator updated to %s.%s\n", colorCyan, *operator, colorReset)
				}
				continue

			case "/clear":
				fmt.Print("\033[H\033[2J")
				printMartianBanner(*operator, *sol, *portPath, *byteDelay)
				continue

			case "/help":
				fmt.Println(colorCyan + "\nAvailable Commands:" + colorReset)
				fmt.Println("  /delay <dur>    - Set byte trickle delay (e.g. /delay 35ms, /delay 0s)")
				fmt.Println("  /sol <n>        - Update current Martian Sol day")
				fmt.Println("  /operator <name>- Change active operator / callsign")
				fmt.Println("  /clear          - Clear terminal screen and re-render header")
				fmt.Println("  /help           - Display this help message")
				fmt.Println("  /exit           - Terminate comms terminal")
				fmt.Println()
				continue

			default:
				fmt.Printf("%s[SYSTEM] Unknown command %s. Type /help for assistance.%s\n", colorRed, parts[0], colorReset)
				continue
			}
		}

		// Format payload with Martian telemetry header
		timestamp := time.Now().Format("15:04:05")
		payload := fmt.Sprintf("[%s | SOL %d | %s] %s", *operator, *sol, timestamp, line)
		payloadBytes := []byte(payload)

		frameBytes, err := protocol.Encode(seq, payloadBytes)
		if err != nil {
			fmt.Printf("%s[ERROR] Failed to encode frame: %v%s\n", colorRed, err, colorReset)
			continue
		}

		// Live build-up of raw frame bytes on the wire
		fmt.Printf("  %s📡 Wire Stream:%s [", colorAmber, colorReset)
		os.Stdout.Sync()

		singleByteBuf := []byte{0}
		for i, b := range frameBytes {
			singleByteBuf[0] = b
			_, writeErr := port.Write(singleByteBuf)
			if writeErr != nil {
				fmt.Printf(" %s[TX FAILED: %v]%s", colorRed, writeErr, colorReset)
				break
			}

			// Print colored byte as it hits the wire
			fieldColor := getFieldColor(i, len(payloadBytes))
			fmt.Printf("%s%02X%s", fieldColor, b, colorReset)
			if i+1 < len(frameBytes) {
				fmt.Print(" ")
			}
			os.Stdout.Sync()

			if *byteDelay > 0 && i+1 < len(frameBytes) {
				time.Sleep(*byteDelay)
			}
		}
		fmt.Printf("]\n")

		// Visual confirmation summary
		fmt.Printf("  %s↳ [TX CONFIRMED]%s %d bytes wire | Seq #%d | Checksum: 0x%08X\n\n",
			colorGreen, colorReset, len(frameBytes), seq, protocol.CRCSize)

		seq++
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Input error: %v", err)
	}
}
