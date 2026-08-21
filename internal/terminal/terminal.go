package terminal

import (
	"fmt"
	"strings"
)

// ANSI color codes for vintage terminal aesthetic
const (
	Reset   = "\033[0m"
	Green   = "\033[1;32m"
	Amber   = "\033[1;33m"
	Cyan    = "\033[1;36m"
	Red     = "\033[1;31m"
	Magenta = "\033[1;35m"
	Gray    = "\033[38;5;245m"
	Dim     = "\033[2m"
	Bold    = "\033[1m"
	Orange  = "\033[38;5;208m"
)

// ClearLine returns the ANSI sequence to move to the beginning of the line and clear it.
const ClearLine = "\r\033[K"

// ColorizeByte returns the syntax highlighted hex string for a byte based on its frame offset.
// Frame layout: [Sync: 0..1] [Seq: 2..5] [Len: 6..7] [Payload: 8..8+N] [CRC: end]
func ColorizeByte(idx, payloadLen int, b byte) string {
	var color string
	switch {
	case idx < 2:
		color = Cyan // Sync Word (0xAA 0x55)
	case idx < 6:
		color = Amber // Sequence ID (4 bytes)
	case idx < 8:
		color = Magenta // Payload Length (2 bytes)
	case idx < 8+payloadLen:
		color = Green // Payload bytes
	default:
		color = Orange // CRC32 Checksum (4 bytes)
	}
	return fmt.Sprintf("%s%02X%s", color, b, Reset)
}

// PrintBanner renders the common ASCII mission header.
func PrintBanner(title, subtitle, station, port string, extraFields ...[2]string) {
	fmt.Print(Orange)
	fmt.Println(`  ____       _   _        __ _           _             `)
	fmt.Println(` |  _ \ __ _| |_| |__   / _(_)_ __   __| | ___ _ __   `)
	fmt.Println(` | |_) / _` + "`" + ` | __| '_ \ | |_| | '_ \ / _` + "`" + ` |/ _ \ '__|  `)
	fmt.Println(` |  __/ (_| | |_| | | ||  _| | | | | (_| |  __/ |     `)
	fmt.Println(` |_|   \__,_|\__|_| |_||_| |_|_| |_|\__,_|\___|_|     `)
	fmt.Printf("        %s\n", subtitle)
	fmt.Print(Reset)
	fmt.Println(Dim + strings.Repeat("─", 64) + Reset)
	fmt.Printf("%s[STATION]%s    %s%s%s\n", Amber, Reset, Bold, station, Reset)
	fmt.Printf("%s[PORT]%s       %s\n", Amber, Reset, port)

	for _, field := range extraFields {
		fmt.Printf("%s[%s]%s%s\n", Amber, field[0], Reset, field[1])
	}

	fmt.Println(Dim + strings.Repeat("─", 64) + Reset)
	fmt.Println()
}
