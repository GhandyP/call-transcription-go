// Command call-transcription-go cleans and validates supplied call transcript
// segments and writes the validated transcript as JSON on stdout.
//
// The command reads one CallInput document, runs the deterministic cleanup and,
// unless -baseline is given, one Google ADK agent turn over its result. It never
// reads audio, never writes to disk, and never claims to have transcribed
// anything: a recording is only ever referenced.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/GhandyP/call-transcription-go/adapters/stt"
	"github.com/GhandyP/call-transcription-go/internal/agent"
	"github.com/GhandyP/call-transcription-go/internal/cleanup"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// Exit codes of the command. They are part of the command contract: 0 for a
// transcript, 1 for a failure, 2 for a usage error. A transcript that needs a
// human review still exits 0, because a review gate is data, not a failure.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// stdinReader is read when the input argument is "-". It is a seam so the
// command can be tested without touching the real standard input.
var stdinReader io.Reader = os.Stdin

// newCleaner builds the model path. It is a seam: the tests inject an offline
// model through it, so the command is verified without credentials.
var newCleaner = func(ctx context.Context, cfg agent.Config) (*agent.Cleaner, error) {
	return agent.NewCleaner(ctx, cfg)
}

const usage = `usage: call-transcription-go [flags] input.json

Reads one CallInput JSON document, normalizes and validates its transcript
segments, and writes the resulting transcript as JSON on stdout. Use "-" to read
the document from standard input.

Flags:
  -baseline            run the deterministic path only and never call a model
  -model string        Gemini model for the cleanup agent (default ` + agent.DefaultModel + `)
  -stt-fixture string  offline fixture; requires an audioRef and no input segments

The command never reads audio and never writes to disk. With -baseline, and for
a call that carries no segments, no model is called at all. A supplied
-stt-fixture must be non-empty and is valid only when the input has an audioRef
and no segments.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run keeps main testable and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("call-transcription-go", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(stderr, usage) }

	baseline := flags.Bool("baseline", false, "run the deterministic path only and never call a model")
	modelName := flags.String("model", "", "Gemini model name for the cleanup agent")
	fixture := flags.String("stt-fixture", "", "offline fixture; requires input with audioRef and no segments")

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	var modelSpecified, fixtureSpecified bool
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "model":
			modelSpecified = true
		case "stt-fixture":
			fixtureSpecified = true
		}
	})
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "call-transcription-go: exactly one input document is required, got %d\n\n", flags.NArg())
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	// -model next to -baseline is a contradiction, not something to ignore
	// quietly: the caller has to know which path ran.
	if *baseline && modelSpecified {
		fmt.Fprintln(stderr, "call-transcription-go: -baseline never calls a model, so -model cannot be combined with it")
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	if fixtureSpecified && *fixture == "" {
		fmt.Fprintln(stderr, "call-transcription-go: -stt-fixture requires a non-empty path")
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	input, err := readInput(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "call-transcription-go: %v\n", err)
		return exitFailure
	}

	if fixtureSpecified && (input.AudioRef == nil || len(input.Segments) > 0) {
		fmt.Fprintln(stderr, "call-transcription-go: -stt-fixture requires an input with audioRef and no segments")
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	ctx := context.Background()

	// An input that references a recording and carries no segments needs a
	// transcription step before anything else can happen.
	if len(input.Segments) == 0 && input.AudioRef != nil {
		if !fixtureSpecified {
			fmt.Fprintf(stderr, "call-transcription-go: call %q references %q but carries no segments, and this build ships an offline transcriber only; pass -stt-fixture <path> with a JSON segment fixture\n",
				input.CallID, *input.AudioRef)
			return exitFailure
		}
		transcribed, err := transcribe(ctx, *fixture, input)
		if err != nil {
			fmt.Fprintf(stderr, "call-transcription-go: %v\n", err)
			return exitFailure
		}
		input = transcribed
	}

	out, err := produce(ctx, *baseline, *modelName, input)
	if err != nil {
		fmt.Fprintf(stderr, "call-transcription-go: %v\n", err)
		return exitFailure
	}

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "call-transcription-go: encode the transcript: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "%s\n", encoded)
	return exitOK
}

// readInput reads one CallInput document from a path, or from standard input
// when the path is "-".
func readInput(path string) (transcript.CallInput, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		raw, err = io.ReadAll(stdinReader)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return transcript.CallInput{}, fmt.Errorf("read the input document: %w", err)
	}

	var input transcript.CallInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return transcript.CallInput{}, fmt.Errorf("decode the input document: %w", err)
	}
	return input, nil
}

// transcribe fills the segments of an audio-only input with the offline
// transcriber. Only the reference travels: the recording is never opened.
func transcribe(ctx context.Context, fixturePath string, input transcript.CallInput) (transcript.CallInput, error) {
	transcriber, err := stt.NewFakeFromFile(fixturePath)
	if err != nil {
		return transcript.CallInput{}, err
	}
	// Refuse an adapter that admits it keeps audio: the command's contract is
	// that nothing from a recording is read or stored.
	if retention := transcriber.Retention(); retention != stt.RetentionNone {
		return transcript.CallInput{}, fmt.Errorf("the transcriber reports retention %d, which this command refuses", retention)
	}

	result, err := transcriber.Transcribe(ctx, stt.Request{CallID: input.CallID, AudioRef: *input.AudioRef})
	if err != nil {
		return transcript.CallInput{}, fmt.Errorf("transcribe the call: %w", err)
	}

	input.Segments = result.Segments
	return input, nil
}

// produce runs the deterministic path, or the deterministic path followed by
// one agent turn.
func produce(ctx context.Context, baseline bool, modelName string, input transcript.CallInput) (transcript.Transcript, error) {
	if baseline {
		return cleanup.Clean(input, cleanup.DefaultOptions())
	}

	cleaner, err := newCleaner(ctx, agent.Config{ModelName: modelName})
	if err != nil {
		return transcript.Transcript{}, err
	}
	return cleaner.Clean(ctx, input)
}
