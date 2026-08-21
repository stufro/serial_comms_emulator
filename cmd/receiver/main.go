package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/stufro/serial-protocol/protocol"
	"github.com/stufro/serial-protocol/transport"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[1;32m"
	colorAmber  = "\033[1;33m"
	colorCyan   = "\033[1;36m"
	colorRed    = "\033[1;31m"
	colorGray   = "\033[38;5;245m" // Light grey for in-flight temporary state
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
	colorOrange = "\033[38;5;208m"
)

func printReceiverBanner(station string, port string) {
	fmt.Print(colorCyan)
	fmt.Println(`  ____       _   _        __ _           _             `)
	fmt.Println(` |  _ \ __ _| |_| |__   / _(_)_ __   __| | ___ _ __   `)
	fmt.Println(` | |_) / _` + "`" + ` | __| '_ \ | |_| | '_ \ / _` + "`" + ` |/ _ \ '__|  `)
	fmt.Println(` |  __/ (_| | |_| | | ||  _| | | | | (_| |  __/ |     `)
	fmt.Println(` |_|   \__,_|\__|_| |_||_| |_|_| |_|\__,_|\___|_|     `)
	fmt.Println(`          DEEP SPACE NETWORK TELEMETRY RECEIVER       `)
	fmt.Print(colorReset)
	fmt.Println(colorDim + strings.Repeat("─", 64) + colorReset)
	fmt.Printf("%s[STATION]%s  %s%s%s\n", colorAmber, colorReset, colorBold, station, colorReset)
	fmt.Printf("%s[PORT]%s     %s\n", colorAmber, colorReset, port)
	fmt.Printf("%s[STATUS]%s   %sLISTENING FOR SYNC (0xAA 0x55)...%s\n", colorAmber, colorReset, colorGreen, colorReset)
	fmt.Println(colorDim + strings.Repeat("─", 64) + colorReset)
	fmt.Println()
}

func main() {
	portPath := flag.String("port", "/tmp/ttyV1", "Serial device or PTY path")
	baudRate := flag.Int("baud", 115200, "Baud rate")
	station := flag.String("station", "JPL MISSION CONTROL (PASADENA, CA)", "Receiver station identifier")
	flag.Parse()

	port, err := transport.OpenPort(*portPath, *baudRate)
	if err != nil {
		log.Fatalf("Failed to open port %s: %v", *portPath, err)
	}
	defer port.Close()

	printReceiverBanner(*station, *portPath)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	decoder := protocol.NewDecoder(port)

	var mu sync.Mutex

	// Attach progress callback to render sliding last-32-bytes in light grey on a single line
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

		// \r = return to line start, \033[K = clear line
		fmt.Printf("\r\033[K%s⏳ [INCOMING STREAM] %s | Raw: [%s]%s", colorGray, stage, hexPreview, colorReset)
		os.Stdout.Sync()
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
		case frame := <-frameChan:
			mu.Lock()
			totalFrames++

			// Clear temporary light grey progress line
			fmt.Print("\r\033[K")

			// Print finalized transmission banner
			fmt.Printf("%s▼ [INCOMING TRANSMISSION]%s Frame #%03d | CRC: 0x%08X %s[OK]%s | Len: %d B\n",
				colorGreen, colorReset, frame.Seq, frame.CRC, colorGreen, colorReset, len(frame.Payload))
			fmt.Printf("  %s%s%s%s\n\n", colorBold, colorAmber, string(frame.Payload), colorReset)
			os.Stdout.Sync()
			mu.Unlock()

		case err := <-errChan:
			mu.Lock()
			fmt.Print("\r\033[K")
			if errors.Is(err, io.EOF) {
				fmt.Printf("\n%s[COMMS LOST] Stream closed (EOF).%s\n", colorRed, colorReset)
				mu.Unlock()
				return
			}
			fmt.Printf("%s[PARSER WARNING] %v%s\n", colorRed, err, colorReset)
			mu.Unlock()

		case <-sigChan:
			mu.Lock()
			fmt.Print("\r\033[K")
			fmt.Printf("\n%s[RECEIVER OFFLINE] Total messages decoded: %d%s\n", colorAmber, totalFrames, colorReset)
			mu.Unlock()
			return
		}
	}
}
