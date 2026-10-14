// Package cleanup rewrites transcript text deterministically.
//
// It is the model-free half of the pipeline: Clean normalizes the call with the
// transcript contract, rewrites only the text of each segment, and then proves
// with transcript.ValidateOutput that nothing else moved. Identity, speaker,
// time range and confidence come out exactly as they went in.
//
// Two policies are applied to the text, and both are conservative by default:
//
//   - Redaction replaces sensitive values (credentials, identity numbers, long
//     high-entropy tokens) with readable placeholders and records which rule
//     matched. It never records the value itself.
//   - The profanity policy flags matches without touching the words; callers
//     that need censorship opt in explicitly.
//
// A placeholder is not a guarantee: a rule-based filter cannot recognise every
// possible secret, so sensitive material should not be pasted into a transcript
// in the first place. Redaction is defence in depth, not a seal.
package cleanup

import "github.com/GhandyP/call-transcription-go/internal/transcript"

// RedactionPolicy selects what happens to sensitive values in segment text.
type RedactionPolicy int

const (
	// RedactSensitive replaces sensitive values with placeholders. This is the
	// default and the zero value.
	RedactSensitive RedactionPolicy = iota
	// RedactOff leaves the text of a call that was already redacted upstream.
	// Callers that choose it take responsibility for the content.
	RedactOff
)

// ProfanityPolicy selects what happens to matched profanity.
type ProfanityPolicy int

const (
	// ProfanityFlag keeps the words and records a finding. This is the default
	// and the zero value: silently rewriting what a customer said would change
	// the evidence a reviewer has to judge.
	ProfanityFlag ProfanityPolicy = iota
	// ProfanityRedact replaces each matched word with PlaceholderProfanity.
	ProfanityRedact
)

// Placeholders written in place of a redacted value. They name the rule that
// matched, never the value.
const (
	PlaceholderEmail      = "[redacted:email]"
	PlaceholderSSN        = "[redacted:ssn]"
	PlaceholderCard       = "[redacted:card]"
	PlaceholderAPIKey     = "[redacted:api_key]"
	PlaceholderToken      = "[redacted:token]"
	PlaceholderPrivateKey = "[redacted:private_key]"
	PlaceholderProfanity  = "[redacted:profanity]"
)

// Options configures the deterministic cleanup.
type Options struct {
	// Transcript carries the thresholds of the contract rules. The zero value
	// selects the documented defaults.
	Transcript transcript.Options
	// Redaction defaults to RedactSensitive.
	Redaction RedactionPolicy
	// Profanity defaults to ProfanityFlag.
	Profanity ProfanityPolicy
}

// DefaultOptions returns the documented defaults: contract defaults, sensitive
// values redacted, profanity flagged but kept.
func DefaultOptions() Options {
	return Options{
		Transcript: transcript.DefaultOptions(),
		Redaction:  RedactSensitive,
		Profanity:  ProfanityFlag,
	}
}
