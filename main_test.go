package main

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/GhandyP/call-transcription-go/internal/agent"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// fakeLLM lets the command's model path run offline, with no credentials.
type fakeLLM struct {
	answer string
	calls  int
}

func (m *fakeLLM) Name() string { return "fake-gemini" }

func (m *fakeLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText(m.answer, genai.RoleModel),
			TurnComplete: true,
		}, nil)
	}
}

// withFakeModel replaces the cleaner seam for the duration of one test.
func withFakeModel(t *testing.T, answer string) *fakeLLM {
	t.Helper()
	fake := &fakeLLM{answer: answer}

	original := newCleaner
	newCleaner = func(ctx context.Context, cfg agent.Config) (*agent.Cleaner, error) {
		return agent.NewCleaner(ctx, agent.Config{Model: fake})
	}
	t.Cleanup(func() { newCleaner = original })

	return fake
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

const cleanCall = `{"callID":"call-1","segments":[
 {"id":"s1","speaker":"Customer","start":0,"end":6,"text":"  i need   to change my plan !!!  ","confidence":0.94},
 {"id":"s2","speaker":"Agent","start":6,"end":20,"text":"let me pull up your account","confidence":0.9}
]}`

const expectedUsage = `usage: call-transcription-go [flags] input.json

Reads one CallInput JSON document, normalizes and validates its transcript
segments, and writes the resulting transcript as JSON on stdout. Use "-" to read
the document from standard input.

Flags:
  -baseline            run the deterministic path only and never call a model
  -model string        Gemini model for the cleanup agent (default gemini-flash-latest)
  -stt-fixture string  offline fixture; requires an audioRef and no input segments

The command never reads audio and never writes to disk. With -baseline, and for
a call that carries no segments, no model is called at all. A supplied
-stt-fixture must be non-empty and is valid only when the input has an audioRef
and no segments.
`

func expectedUsageError(message string) string {
	return "call-transcription-go: " + message + "\n" + expectedUsage
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	if string(got) != want {
		t.Errorf("%s contents changed: got %q, want %q", filepath.Base(path), got, want)
	}
}

// runCommand runs the command with captured streams.
func runCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func decodeTranscript(t *testing.T, raw string) transcript.Transcript {
	t.Helper()
	var out transcript.Transcript
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode the transcript output: %v\noutput: %s", err, raw)
	}
	return out
}

func TestRunBaselinePrintsTheValidatedTranscriptAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	input := writeFile(t, dir, "call.json", cleanCall)

	code, stdout, stderr := runCommand(t, "-baseline", input)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	out := decodeTranscript(t, stdout)
	if got, want := len(out.Segments), 2; got != want {
		t.Fatalf("segments = %d, want %d", got, want)
	}
	if got, want := out.Segments[0].Text, "i need to change my plan!"; got != want {
		t.Errorf("segment s1 Text = %q, want %q", got, want)
	}
	if out.Segments[0].ID != "s1" || out.Segments[1].ID != "s2" {
		t.Errorf("segment identity changed: %+v", out.Segments)
	}
	if out.CallID != "call-1" {
		t.Errorf("CallID = %q, want %q", out.CallID, "call-1")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "call.json" {
		t.Errorf("the command wrote to disk: %v", entries)
	}
}

func TestRunKeepsTheReviewGateVisibleWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	input := writeFile(t, dir, "low.json", `{"callID":"call-low","segments":[
	 {"id":"s1","speaker":"Customer","start":0,"end":6,"text":"hello","confidence":0.4}
	]}`)

	code, stdout, stderr := runCommand(t, "-baseline", input)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d: a human decision is not a command failure (stderr: %s)", code, exitOK, stderr)
	}
	out := decodeTranscript(t, stdout)
	if !out.ReviewRequired {
		t.Error("reviewRequired = false, want true for a low confidence call")
	}
	if len(out.Warnings) == 0 {
		t.Error("warnings = none, want the low confidence finding")
	}
}

func TestRunRejectsBaselineCombinedWithAnExplicitModel(t *testing.T) {
	dir := t.TempDir()
	inputContents := cleanCall
	input := writeFile(t, dir, "call.json", inputContents)

	tests := []struct {
		name      string
		modelName string
	}{
		{name: "nonempty model", modelName: "gemini-flash-latest"},
		{name: "explicit empty model", modelName: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := withFakeModel(t, `{"segments":[]}`)
			code, stdout, stderr := runCommand(t, "-baseline", "-model", tt.modelName, input)

			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			wantStderr := expectedUsageError("-baseline never calls a model, so -model cannot be combined with it")
			if stderr != wantStderr {
				t.Errorf("stderr = %q, want exactly %q", stderr, wantStderr)
			}
			if fake.calls != 0 {
				t.Errorf("model calls = %d, want 0 for a rejected request", fake.calls)
			}
			assertFileContents(t, input, inputContents)
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	dir := t.TempDir()
	input := writeFile(t, dir, "call.json", cleanCall)

	tests := []struct {
		name string
		args []string
	}{
		{name: "no input", args: nil},
		{name: "unknown flag", args: []string{"-nope", input}},
		{name: "two inputs", args: []string{input, input}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCommand(t, tt.args...)
			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if !strings.Contains(stderr, "usage: call-transcription-go") {
				t.Errorf("stderr = %q, want the usage line", stderr)
			}
		})
	}
}

func TestRunRejectsAnUnusableInput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "malformed json",
			content: `{"callID": "call-1",`,
			want:    "decode",
		},
		{
			name: "duplicate segment id",
			content: `{"callID":"call-1","segments":[
			 {"id":"s1","speaker":"Agent","start":0,"end":1,"text":"hello","confidence":0.9},
			 {"id":"s1","speaker":"Agent","start":1,"end":2,"text":"again","confidence":0.9}
			]}`,
			want: "duplicate segment id",
		},
		{
			name: "missing call id",
			content: `{"segments":[
			 {"id":"s1","speaker":"Agent","start":0,"end":1,"text":"hello","confidence":0.9}
			]}`,
			want: "callID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			input := writeFile(t, dir, "call.json", tt.content)

			code, stdout, stderr := runCommand(t, "-baseline", input)

			if code != exitFailure {
				t.Errorf("exit code = %d, want %d", code, exitFailure)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want it to mention %q", stderr, tt.want)
			}
		})
	}
}

func TestRunReadsTheInputFromStdin(t *testing.T) {
	original := stdinReader
	stdinReader = strings.NewReader(cleanCall)
	t.Cleanup(func() { stdinReader = original })

	code, stdout, stderr := runCommand(t, "-baseline", "-")

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if got := decodeTranscript(t, stdout).Segments[0].ID; got != "s1" {
		t.Errorf("segment 0 ID = %q, want %q", got, "s1")
	}
}

func TestRunNeedsAnOfflineTranscriberForAnAudioOnlyInput(t *testing.T) {
	dir := t.TempDir()
	inputContents := `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[]}`
	input := writeFile(t, dir, "audio.json", inputContents)

	code, stdout, stderr := runCommand(t, "-baseline", input)

	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	wantStderr := "call-transcription-go: call \"call-1\" references \"s3://recordings/call-1.wav\" but carries no segments, and this build ships an offline transcriber only; pass -stt-fixture <path> with a JSON segment fixture\n"
	if stderr != wantStderr {
		t.Errorf("stderr = %q, want exactly %q", stderr, wantStderr)
	}
	assertFileContents(t, input, inputContents)
}

func TestRunRejectsAnExplicitFixtureThatCannotBeUsed(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "input has no audio reference",
			content: `{"callID":"call-1","segments":[]}`,
		},
		{
			name:    "input already has segments",
			content: `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[{"id":"s1","speaker":"Agent","start":0,"end":1,"text":"hello","confidence":0.9}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			input := writeFile(t, dir, "call.json", tt.content)
			fixture := filepath.Join(dir, "must-not-be-read.json")
			fake := withFakeModel(t, `{"segments":[{"id":"s1","text":"Hello."}]}`)

			code, stdout, stderr := runCommand(t, "-stt-fixture", fixture, input)

			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			wantStderr := expectedUsageError("-stt-fixture requires an input with audioRef and no segments")
			if stderr != wantStderr {
				t.Errorf("stderr = %q, want exactly %q", stderr, wantStderr)
			}
			if fake.calls != 0 {
				t.Errorf("model calls = %d, want 0 for a rejected request", fake.calls)
			}
			if _, err := os.Stat(fixture); !os.IsNotExist(err) {
				t.Errorf("fixture should not have been opened or created: stat error = %v", err)
			}
			assertFileContents(t, input, tt.content)
		})
	}
}

func TestRunRejectsAnExplicitEmptyFixturePath(t *testing.T) {
	dir := t.TempDir()
	inputContents := `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[]}`
	input := writeFile(t, dir, "audio.json", inputContents)
	fake := withFakeModel(t, `{"segments":[]}`)

	code, stdout, stderr := runCommand(t, "-stt-fixture", "", input)

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	wantStderr := expectedUsageError("-stt-fixture requires a non-empty path")
	if stderr != wantStderr {
		t.Errorf("stderr = %q, want exactly %q", stderr, wantStderr)
	}
	if fake.calls != 0 {
		t.Errorf("model calls = %d, want 0 for a rejected request", fake.calls)
	}
	assertFileContents(t, input, inputContents)
}

func TestRunUsesTheOfflineTranscriberWhenAFixtureIsGiven(t *testing.T) {
	dir := t.TempDir()
	inputContents := `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[]}`
	fixtureContents := `[
	 {"id":"","speaker":"Agent","start":0,"end":4,"text":"Thanks for calling.","confidence":0.92},
	 {"id":"","speaker":"Customer","start":4,"end":9,"text":"I need help with my bill.","confidence":0.9}
	]`
	input := writeFile(t, dir, "audio.json", inputContents)
	fixture := writeFile(t, dir, "fixture.json", fixtureContents)

	code, stdout, stderr := runCommand(t, "-baseline", "-stt-fixture", fixture, input)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	out := decodeTranscript(t, stdout)
	if got, want := len(out.Segments), 2; got != want {
		t.Fatalf("segments = %d, want %d", got, want)
	}
	if out.Segments[0].ID != "s1" || out.Segments[1].ID != "s2" {
		t.Errorf("the offline transcriber did not stamp stable ids: %+v", out.Segments)
	}
	if out.AudioRef == nil || *out.AudioRef != "s3://recordings/call-1.wav" {
		t.Errorf("AudioRef = %v, want the source reference", out.AudioRef)
	}
	assertFileContents(t, input, inputContents)
	assertFileContents(t, fixture, fixtureContents)
}

func TestRunModelPathPrintsTheValidatedTranscript(t *testing.T) {
	fake := withFakeModel(t, `{"segments":[
		{"id":"s1","text":"I need to change my plan."},
		{"id":"s2","text":"Let me pull up your account."}
	]}`)

	dir := t.TempDir()
	input := writeFile(t, dir, "call.json", cleanCall)

	code, stdout, stderr := runCommand(t, input)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	out := decodeTranscript(t, stdout)
	if got, want := out.Segments[0].Text, "I need to change my plan."; got != want {
		t.Errorf("segment s1 Text = %q, want %q", got, want)
	}
	if fake.calls != 1 {
		t.Errorf("model calls = %d, want exactly one", fake.calls)
	}
}

func TestRunRejectsAModelAnswerThatDropsASegment(t *testing.T) {
	withFakeModel(t, `{"segments":[{"id":"s1","text":"I need to change my plan."}]}`)

	dir := t.TempDir()
	input := writeFile(t, dir, "call.json", cleanCall)

	code, stdout, stderr := runCommand(t, input)

	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing from a rejected answer", stdout)
	}
	if !strings.Contains(stderr, "does not cover every source segment") {
		t.Errorf("stderr = %q, want the rejection reason", stderr)
	}
}

func TestRunFailsHonestlyWithoutCredentials(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("ADK_MODEL", "")

	dir := t.TempDir()
	input := writeFile(t, dir, "call.json", cleanCall)

	code, stdout, stderr := runCommand(t, input)

	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, "GOOGLE_API_KEY") {
		t.Errorf("stderr = %q, want it to name the missing credential", stderr)
	}
}
