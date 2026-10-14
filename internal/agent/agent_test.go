package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/GhandyP/call-transcription-go/internal/agent"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// fakeModel is an injected model.LLM: the whole agent layer is exercised
// offline, with no credentials and no network.
type fakeModel struct {
	name    string
	answer  string
	failure error

	calls   int
	prompts []string
}

func (m *fakeModel) Name() string { return m.name }

func (m *fakeModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		m.prompts = append(m.prompts, requestText(req))

		if m.failure != nil {
			yield(nil, m.failure)
			return
		}
		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText(m.answer, genai.RoleModel),
			TurnComplete: true,
		}, nil)
	}
}

func requestText(req *model.LLMRequest) string {
	var builder strings.Builder
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part != nil {
				builder.WriteString(part.Text)
			}
		}
	}
	return builder.String()
}

// reference returns a cleaned transcript, the shape the agent receives.
func reference(t *testing.T) transcript.Transcript {
	t.Helper()
	return transcript.Transcript{
		CallID: "call-1",
		Segments: []transcript.Segment{
			{ID: "s1", Speaker: "Customer", Start: 0, End: 6, Text: "i need to change my plan", Confidence: 0.94},
			{ID: "s2", Speaker: "Agent", Start: 6, End: 20, Text: "let me pull up your account", Confidence: 0.9},
		},
		Warnings: []transcript.Warning{
			transcript.NewWarning(transcript.CodeLowConfidence, "s2", "confidence 0.68 is below the 0.70 threshold"),
		},
		ReviewRequired: true,
	}
}

func TestInstructionStatesTheTextOnlyBoundary(t *testing.T) {
	instruction := agent.Instruction()
	for _, want := range []string{"already text", "audio", "id", "[redacted:", "transcribe"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("Instruction() does not mention %q", want)
		}
	}
}

func TestOutputSchemaLetsTheModelChangeTextOnly(t *testing.T) {
	schema := agent.OutputSchema()

	if schema.Type != genai.TypeObject {
		t.Fatalf("schema type = %v, want an object", schema.Type)
	}
	segments, ok := schema.Properties["segments"]
	if !ok || segments.Type != genai.TypeArray {
		t.Fatalf("schema has no segments array: %+v", schema.Properties)
	}

	item := segments.Items
	for _, field := range []string{"id", "text"} {
		if _, ok := item.Properties[field]; !ok {
			t.Errorf("item schema has no %q property", field)
		}
	}
	if len(item.Properties) != 2 {
		t.Errorf("item schema exposes %d properties %v, want exactly id and text", len(item.Properties), item.Properties)
	}
	required := strings.Join(item.Required, ",")
	if !strings.Contains(required, "id") || !strings.Contains(required, "text") {
		t.Errorf("item schema requires %v, want id and text", item.Required)
	}
}

func TestPromptCarriesOnlyTheSegmentIdentityAndText(t *testing.T) {
	prompt, err := agent.Prompt(reference(t))
	if err != nil {
		t.Fatalf("Prompt() error = %v, want nil", err)
	}

	for _, want := range []string{`"s1"`, `"s2"`, "i need to change my plan", "let me pull up your account"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Prompt() does not carry %s", want)
		}
	}
	for _, unwanted := range []string{"Customer", "Agent", "confidence", "0.94"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("Prompt() leaked %q to the model", unwanted)
		}
	}
}

func TestParseAnswerAcceptsATextRewrite(t *testing.T) {
	ref := reference(t)
	raw := []byte(`{"segments":[
		{"id":"s2","text":"Let me pull up your account."},
		{"id":"s1","text":"I need to change my plan."}
	]}`)

	out, err := agent.ParseAnswer(ref, raw)
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v, want nil", err)
	}
	if got, want := out.Segments[0].Text, "I need to change my plan."; got != want {
		t.Errorf("segment s1 Text = %q, want %q", got, want)
	}
	if got, want := out.Segments[1].Text, "Let me pull up your account."; got != want {
		t.Errorf("segment s2 Text = %q, want %q", got, want)
	}
	if out.Segments[0].Speaker != "Customer" || out.Segments[0].Start != 0 || out.Segments[0].End != 6 {
		t.Errorf("ParseAnswer() changed a source fact: %+v", out.Segments[0])
	}
	if !out.ReviewRequired || len(out.Warnings) != 1 {
		t.Errorf("ParseAnswer() dropped the deterministic findings: %+v", out.Warnings)
	}
}

func TestParseAnswerRejectsAnAnswerThatDoesNotFitTheSource(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{
			name: "not json",
			raw:  `the segments look fine to me`,
			want: agent.ErrMalformedAnswer,
		},
		{
			name: "no segments",
			raw:  `{"segments":[]}`,
			want: agent.ErrMalformedAnswer,
		},
		{
			name: "dropped segment",
			raw:  `{"segments":[{"id":"s1","text":"I need to change my plan."}]}`,
			want: agent.ErrMissingSegmentText,
		},
		{
			name: "invented segment",
			raw: `{"segments":[
				{"id":"s1","text":"I need to change my plan."},
				{"id":"s2","text":"Let me pull up your account."},
				{"id":"s9","text":"Anything else?"}
			]}`,
			want: agent.ErrUnknownSegmentID,
		},
		{
			name: "repeated segment",
			raw: `{"segments":[
				{"id":"s1","text":"I need to change my plan."},
				{"id":"s1","text":"I need to change my plan, please."},
				{"id":"s2","text":"Let me pull up your account."}
			]}`,
			want: agent.ErrDuplicateSegmentText,
		},
		{
			name: "blanked segment",
			raw: `{"segments":[
				{"id":"s1","text":"   "},
				{"id":"s2","text":"Let me pull up your account."}
			]}`,
			want: transcript.ErrBlankedText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := agent.ParseAnswer(reference(t), []byte(tt.raw))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseAnswer() error = %v, want %v", err, tt.want)
			}
			if len(out.Segments) != 0 {
				t.Errorf("ParseAnswer() returned %d segments with an error, want none", len(out.Segments))
			}
		})
	}
}

func TestCleanRunsOneTurnOverCleanedTextAndValidatesTheAnswer(t *testing.T) {
	fake := &fakeModel{name: "fake-gemini", answer: `{"segments":[
		{"id":"s1","text":"I need to change my plan."},
		{"id":"s2","text":"Let me pull up your account."}
	]}`}

	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	in := transcript.CallInput{
		CallID: "call-1",
		Segments: []transcript.Segment{
			{ID: "s1", Speaker: "Customer", Start: 0, End: 6, Text: "  i need   to change my plan !!  ", Confidence: 0.94},
			{ID: "s2", Speaker: "Agent", Start: 6, End: 20, Text: "let me pull up your account", Confidence: 0.9},
		},
	}

	out, err := cleaner.Clean(context.Background(), in)
	if err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}

	if fake.calls != 1 {
		t.Errorf("model calls = %d, want exactly one", fake.calls)
	}
	if len(fake.prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(fake.prompts))
	}
	// The model must see the cleaned text, never the raw text.
	if strings.Contains(fake.prompts[0], "!!") || strings.Contains(fake.prompts[0], "  ") {
		t.Errorf("the prompt carried uncleaned text: %q", fake.prompts[0])
	}
	if got, want := out.Segments[0].Text, "I need to change my plan."; got != want {
		t.Errorf("segment s1 Text = %q, want %q", got, want)
	}
	if got, want := out.Segments[1].ID, "s2"; got != want {
		t.Errorf("segment 1 ID = %q, want %q", got, want)
	}
	if out.Segments[0].Confidence != 0.94 || out.Segments[0].Speaker != "Customer" {
		t.Errorf("Clean() changed a source fact: %+v", out.Segments[0])
	}
}

func TestCleanPropagatesAModelFailure(t *testing.T) {
	failure := errors.New("model unavailable")
	fake := &fakeModel{name: "fake-gemini", failure: failure}

	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Customer", Start: 0, End: 6, Text: "hello", Confidence: 0.94},
	}}

	out, err := cleaner.Clean(context.Background(), in)
	if err == nil {
		t.Fatal("Clean() error = nil, want the model failure")
	}
	if !errors.Is(err, failure) {
		t.Errorf("Clean() error = %v, want it to wrap %v", err, failure)
	}
	if len(out.Segments) != 0 {
		t.Errorf("Clean() returned %d segments with an error, want none", len(out.Segments))
	}
}

func TestCleanRejectsAnAnswerThatDropsASegment(t *testing.T) {
	fake := &fakeModel{name: "fake-gemini", answer: `{"segments":[{"id":"s1","text":"I need to change my plan."}]}`}

	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Customer", Start: 0, End: 6, Text: "i need to change my plan", Confidence: 0.94},
		{ID: "s2", Speaker: "Agent", Start: 6, End: 20, Text: "let me pull up your account", Confidence: 0.9},
	}}

	if _, err := cleaner.Clean(context.Background(), in); !errors.Is(err, agent.ErrMissingSegmentText) {
		t.Fatalf("Clean() error = %v, want %v", err, agent.ErrMissingSegmentText)
	}
}

func TestCleanSkipsTheModelWhenThereIsNothingToRewrite(t *testing.T) {
	fake := &fakeModel{name: "fake-gemini", answer: `{"segments":[]}`}

	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	out, err := cleaner.Clean(context.Background(), transcript.CallInput{CallID: "call-silent"})
	if err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}
	if fake.calls != 0 {
		t.Errorf("model calls = %d, want none for a call with no segments", fake.calls)
	}
	if !out.ReviewRequired {
		t.Error("ReviewRequired = false, want true for an empty transcript")
	}
}

func TestNewCleanerRequiresCredentialsWithoutAnInjectedModel(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")

	if _, err := agent.NewCleaner(context.Background(), agent.Config{}); !errors.Is(err, agent.ErrMissingAPIKey) {
		t.Fatalf("NewCleaner() error = %v, want %v", err, agent.ErrMissingAPIKey)
	}
}

func TestParseAnswerIsPureAndDeterministic(t *testing.T) {
	ref := reference(t)
	raw := []byte(`{"segments":[{"id":"s1","text":"I need to change my plan."},{"id":"s2","text":"Let me pull up your account."}]}`)

	first, err := agent.ParseAnswer(ref, raw)
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v, want nil", err)
	}
	second, err := agent.ParseAnswer(ref, raw)
	if err != nil {
		t.Fatalf("ParseAnswer() second error = %v, want nil", err)
	}

	a, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n%s", a, b)
	}
	if ref.Segments[0].Text != "i need to change my plan" {
		t.Errorf("ParseAnswer() mutated the reference: %q", ref.Segments[0].Text)
	}
}
