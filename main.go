// Command call-transcription-go cleans and validates supplied call transcript
// segments and writes the validated transcript as JSON on stdout.
//
// This entry point exists from the first commit so the module builds and the
// usage contract is visible. The command itself is wired in a later work unit
// (T6 of odd/tasks/call-transcription-go.md); until then it reports that the
// command is not implemented and exits with the usage code.
package main

import (
	"fmt"
	"os"
)

// Exit codes of the command. They are part of the command contract.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `usage: call-transcription-go [flags] input.json

Reads one CallInput JSON document, normalizes and validates its transcript
segments, and writes the resulting transcript as JSON on stdout.

Flags:
  -baseline        run the deterministic path only and never call a model
  -model string    Gemini model name for the cleanup agent (default gemini-flash-latest)

The command never reads audio and never writes to disk.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

// run keeps main testable and returns the process exit code.
func run(args []string) int {
	_ = args
	fmt.Fprintln(os.Stderr, "call-transcription-go: the command is not implemented yet")
	fmt.Fprint(os.Stderr, usage)
	return exitUsage
}
