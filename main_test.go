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
	input := writeFile(t, dir, "call.json", cleanCall)

	code, stdout, stderr := runCommand(t, "-baseline", "-model", "gemini-flash-latest", input)

	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, "-model") {
		t.Errorf("stderr = %q, want it to name the rejected flag combination", stderr)
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
	input := writeFile(t, dir, "audio.json", `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[]}`)

	code, stdout, stderr := runCommand(t, "-baseline", input)

	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, "-stt-fixture") {
		t.Errorf("stderr = %q, want it to name the flag that would make this possible", stderr)
	}
}

func TestRunUsesTheOfflineTranscriberWhenAFixtureIsGiven(t *testing.T) {
	dir := t.TempDir()
	input := writeFile(t, dir, "audio.json", `{"callID":"call-1","audioRef":"s3://recordings/call-1.wav","segments":[]}`)
	fixture := writeFile(t, dir, "fixture.json", `[
	 {"id":"","speaker":"Agent","start":0,"end":4,"text":"Thanks for calling.","confidence":0.92},
	 {"id":"","speaker":"Customer","start":4,"end":9,"text":"I need help with my bill.","confidence":0.9}
	]`)

	code, stdout, stderr := runCommand(t, "-baseline", "-stt-fixture", fixture, input)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
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
