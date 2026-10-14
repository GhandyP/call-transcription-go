// Package agent rewrites the text of a cleaned call transcript with one Google
// ADK Go (v2) agent.
//
// The package is deliberately small. Everything that can be checked without
// credentials is a pure function — the instruction, the output schema, the
// prompt, the parser and the validation — and only Clean talks to a model. The
// model handle is injectable, so the whole layer is exercised offline, in tests
// and in CI, with no credentials and no network.
//
// The order is enforced here, not by the caller: Clean runs the deterministic
// cleanup first, so the model only ever sees normalized and redacted text, and
// the answer is checked with transcript.ValidateOutput before it is accepted. A
// model answer that drops, invents, repeats or blanks a segment is rejected,
// never repaired.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/GhandyP/call-transcription-go/internal/cleanup"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

const (
	// DefaultModel is the Gemini model this package documents.
	DefaultModel = "gemini-flash-latest"
	// ModelEnv overrides DefaultModel.
	ModelEnv = "ADK_MODEL"
	// APIKeyEnv is the environment variable the model client reads.
	APIKeyEnv = "GOOGLE_API_KEY"
	// AppName identifies the ADK application that owns the sessions.
	AppName = "call-transcription"

	agentName = "transcript_text_cleaner"
	userID    = "call-transcription-cli"
)

// ErrMissingAPIKey reports a model call attempted without credentials.
var ErrMissingAPIKey = fmt.Errorf("%s is required to call the model", APIKeyEnv)

// ErrNoAgentOutput reports an agent turn that produced no answer at all.
var ErrNoAgentOutput = fmt.Errorf("the agent finished without an answer")

// Config selects the model and the credentials.
type Config struct {
	// Model is the model handle to use. When it is nil, NewCleaner builds a
	// Gemini model from ModelName and APIKey. Injecting a model is what makes
	// this package testable offline.
	Model model.LLM
	// ModelName defaults to ModelEnv and then to DefaultModel.
	ModelName string
	// APIKey defaults to APIKeyEnv.
	APIKey string
}

// Cleaner rewrites the text of a call with one ADK agent.
type Cleaner struct {
	runner *runner.Runner
	runs   atomic.Uint64
}

// NewCleaner builds the single-agent cleaner.
//
// It fails before any network call when no credentials are available and no
// model was injected.
func NewCleaner(ctx context.Context, cfg Config) (*Cleaner, error) {
	llm := cfg.Model
	if llm == nil {
		modelName := firstNonBlank(cfg.ModelName, os.Getenv(ModelEnv), DefaultModel)
		apiKey := firstNonBlank(cfg.APIKey, os.Getenv(APIKeyEnv))
		if apiKey == "" {
			return nil, ErrMissingAPIKey
		}

		built, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{APIKey: apiKey})
		if err != nil {
			return nil, fmt.Errorf("create model %q: %w", modelName, err)
		}
		llm = built
	}

	// No tools: cleaning text must not be able to create, send or publish
	// anything. A future save_transcript tool is an approval-gated decision of
	// its own and is not part of this agent.
	textCleaner, err := llmagent.New(llmagent.Config{
		Name:         agentName,
		Model:        llm,
		Description:  "Rewrites the text of one call transcript segment by segment, without changing any segment identity.",
		Instruction:  Instruction(),
		OutputSchema: OutputSchema(),
	})
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	run, err := runner.NewInMemory(AppName, textCleaner)
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	return &Cleaner{runner: run}, nil
}

// Clean runs the deterministic cleanup and then one model turn over its result.
//
// A call with no segments never reaches a model: there is nothing to rewrite,
// and the deterministic findings already say that a human has to look at it.
func (c *Cleaner) Clean(ctx context.Context, in transcript.CallInput) (transcript.Transcript, error) {
	reference, err := cleanup.Clean(in, cleanup.DefaultOptions())
	if err != nil {
		return transcript.Transcript{}, err
	}
	if len(reference.Segments) == 0 {
		return reference, nil
	}

	raw, err := c.ask(ctx, reference)
	if err != nil {
		return transcript.Transcript{}, err
	}
	return ParseAnswer(reference, raw)
}

// ask runs one turn in a session of its own, so one call's text can never reach
// the next call's prompt.
func (c *Cleaner) ask(ctx context.Context, reference transcript.Transcript) ([]byte, error) {
	prompt, err := Prompt(reference)
	if err != nil {
		return nil, err
	}

	sessionID := fmt.Sprintf("%s-%d", reference.CallID, c.runs.Add(1))
	content := genai.NewContentFromText(prompt, genai.RoleUser)

	var raw, text string
	for event, err := range c.runner.Run(ctx, userID, sessionID, content, agent.RunConfig{}) {
		if err != nil {
			return nil, fmt.Errorf("run agent: %w", err)
		}
		if event == nil {
			continue
		}
		// ADK keeps the value parsed against OutputSchema on the event that
		// closes the turn. Read it wherever it appears and keep the last one,
		// so a differently marked final event cannot make the answer vanish.
		if event.Output != nil {
			encoded, err := json.Marshal(event.Output)
			if err != nil {
				return nil, fmt.Errorf("encode agent output: %w", err)
			}
			raw = string(encoded)
		}
		if event.IsFinalResponse() {
			if answer := eventText(event); answer != "" {
				text = answer
			}
		}
	}

	if raw == "" {
		raw = text
	}
	if raw == "" {
		return nil, ErrNoAgentOutput
	}
	return []byte(raw), nil
}

// eventText returns the text the event carries, if any.
func eventText(event *session.Event) string {
	if event == nil || event.Content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range event.Content.Parts {
		if part != nil {
			builder.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(builder.String())
}

// firstNonBlank returns the first value that is not blank.
func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
