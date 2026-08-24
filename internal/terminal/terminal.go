package terminal

import (
	"fmt"
	"io"
	"strings"
)

// ANSI color codes for vintage terminal aesthetic
const (
	Reset  = "\033[0m"
	Green  = "\033[1;32m"
	Amber  = "\033[1;33m"
	Cyan   = "\033[1;36m"
	Red    = "\033[1;31m"
	Gray   = "\033[38;5;245m"
	Dim    = "\033[2m"
	Bold   = "\033[1m"
	Orange = "\033[38;5;208m"
)

// ClearLine returns the ANSI sequence to move to the beginning of the line and clear it.
const ClearLine = "\r\033[K"

const bannerWidth = 64

// PrintBanner renders the common ASCII mission header to the given writer.
func PrintBanner(out io.Writer, subtitle, station, port string, extraFields ...[2]string) {
	printBannerArt(out, subtitle)

	divider := Dim + strings.Repeat("─", bannerWidth) + Reset
	fmt.Fprintln(out, divider)
	fmt.Fprintf(out, "%s[STATION]%s    %s%s%s\n", Amber, Reset, Bold, station, Reset)
	fmt.Fprintf(out, "%s[PORT]%s       %s\n", Amber, Reset, port)

	for _, field := range extraFields {
		fmt.Fprintf(out, "%s[%s]%s%s\n", Amber, field[0], Reset, field[1])
	}

	fmt.Fprintln(out, divider)
	fmt.Fprintln(out)
}

func printBannerArt(out io.Writer, subtitle string) {
	fmt.Fprint(out, Orange)
	fmt.Fprintln(out, `  ____       _   _        __ _           _             `)
	fmt.Fprintln(out, ` |  _ \ __ _| |_| |__   / _(_)_ __   __| | ___ _ __   `)
	fmt.Fprintln(out, ` | |_) / _`+"`"+` | __| '_ \ | |_| | '_ \ / _`+"`"+` |/ _ \ '__|  `)
	fmt.Fprintln(out, ` |  __/ (_| | |_| | | ||  _| | | | | (_| |  __/ |     `)
	fmt.Fprintln(out, ` |_|   \__,_|\__|_| |_||_| |_|_| |_|\__,_|\___|_|     `)
	fmt.Fprintf(out, "        %s\n", subtitle)
	fmt.Fprint(out, Reset)
}
