package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: go run ./scripts/parity <sync|verify> [--check]")
	}

	switch args[0] {
	case "sync":
		flags := flag.NewFlagSet("sync", flag.ContinueOnError)
		flags.SetOutput(ioDiscard{})
		checkOnly := flags.Bool("check", false, "report parity drift without modifying files")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return syncParityAssets("", *checkOnly)
	case "verify":
		return verifyVendoredParity("")
	default:
		return fmt.Errorf("unknown parity command %q", args[0])
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
