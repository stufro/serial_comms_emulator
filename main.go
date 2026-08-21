package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("================================================================")
		fmt.Println(" Pathfinder Custom Framing Protocol over Virtual Serial Link   ")
		fmt.Println("================================================================")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  1. Create virtual serial link with socat:")
		fmt.Println("     socat PTY,link=/tmp/ttyV0,raw,echo=0 PTY,link=/tmp/ttyV1,raw,echo=0")
		fmt.Println()
		fmt.Println("  2. In Terminal 1 (Receiver):")
		fmt.Println("     go run ./cmd/receiver -port /tmp/ttyV1")
		fmt.Println()
		fmt.Println("  3. In Terminal 2 (Sender):")
		fmt.Println("     go run ./cmd/sender -port /tmp/ttyV0")
		fmt.Println()
		fmt.Println("Or run via subcommands:")
		fmt.Println("  go run main.go sender   [-port /tmp/ttyV0] [-interval 1s] [-count 10]")
		fmt.Println("  go run main.go receiver [-port /tmp/ttyV1]")
		return
	}

	subcommand := os.Args[1]
	args := os.Args[2:]

	var target string
	switch subcommand {
	case "sender", "tx":
		target = "./cmd/sender"
	case "receiver", "rx":
		target = "./cmd/receiver"
	default:
		fmt.Printf("Unknown subcommand: %s (expected 'sender' or 'receiver')\n", subcommand)
		os.Exit(1)
	}

	cmd := exec.Command("go", append([]string{"run", target}, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
