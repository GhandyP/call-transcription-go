package cleanup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// textRewriter rewrites the text of one already normalized segment and reports
// the findings that rewrite produced. It is a seam: Clean passes rewriteText,
// and the package tests pass a step that misbehaves on purpose to prove the
// drift guard is live.
type textRewriter func(segment transcript.Segment, opts Options) (string, []transcript.Warning)

// Clean runs the deterministic cleanup over one call.
//
// It returns either a transcript whose segments keep the identity, speaker,
// range and confidence they arrived with, or an error and no transcript at all.
func Clean(in transcript.CallInput, opts Options) (transcript.Transcript, error) {
	return clean(in, opts, rewriteText)
}

func clean(in transcript.CallInput, opts Options, rewrite textRewriter) (transcript.Transcript, error) {
	normalized, err := transcript.Normalize(in, opts.Transcript)
	if err != nil {
		return transcript.Transcript{}, err
	}

	// The reference for the drift guard is what the caller sees after
	// normalization, not the raw document: Normalize already sorted the
	// segments and named the unknown speakers, and rewriting text must not undo
	// either of those.
	reference := transcript.CallInput{
		CallID:   normalized.CallID,
		AudioRef: normalized.AudioRef,
		Segments: normalized.Segments,
	}

	cleaned := transcript.Transcript{
		CallID:   normalized.CallID,
		AudioRef: normalized.AudioRef,
		// Non-nil empty slices, so a call with no finding and no segment still
		// serializes the arrays the contract promises instead of null.
		Segments: append([]transcript.Segment{}, normalized.Segments...),
		Warnings: append([]transcript.Warning{}, normalized.Warnings...),
	}

	for i := range cleaned.Segments {
		text, findings := rewrite(cleaned.Segments[i], opts)
		cleaned.Segments[i].Text = text
		cleaned.Warnings = append(cleaned.Warnings, findings...)
	}

	transcript.SortWarnings(cleaned.Warnings)
	cleaned.ReviewRequired = transcript.ReviewRequired(cleaned.Warnings)

	if err := transcript.ValidateOutput(reference, cleaned); err != nil {
		return transcript.Transcript{}, err
	}
	return cleaned, nil
}

// rewriteText is the real rewrite step: spacing first, then redaction, then the
// profanity policy.
func rewriteText(segment transcript.Segment, opts Options) (string, []transcript.Warning) {
	var findings []transcript.Warning
	text := normalizeSpacing(segment.Text)

	if opts.Redaction != RedactOff {
		redacted, rules := redactSensitive(text)
		if len(rules) > 0 {
			text = redacted
			findings = append(findings, transcript.NewWarning(transcript.CodeRedacted, segment.ID,
				fmt.Sprintf("replaced %d sensitive value(s) matched by the %s rule(s)", len(rules), strings.Join(rules, ", "))))
		}
	}

	if matches := matchedProfanity(text); len(matches) > 0 {
		if opts.Profanity == ProfanityRedact {
			text = redactProfanity(text)
			findings = append(findings, transcript.NewWarning(transcript.CodeProfanity, segment.ID,
				fmt.Sprintf("the profanity policy matched %d term(s); they were replaced with %s", len(matches), PlaceholderProfanity)))
		} else {
			findings = append(findings, transcript.NewWarning(transcript.CodeProfanity, segment.ID,
				fmt.Sprintf("the profanity policy matched %d term(s); the words are kept", len(matches))))
		}
	}

	return text, findings
}

var (
	whitespaceRun    = regexp.MustCompile(`\s+`)
	spaceBeforePunct = regexp.MustCompile(`\s+([,.;:!?])`)
)

// normalizeSpacing collapses whitespace runs, removes spaces before
// punctuation and collapses a repeated punctuation mark into one. It never
// drops a word, so an utterance can never disappear here.
func normalizeSpacing(text string) string {
	out := whitespaceRun.ReplaceAllString(text, " ")
	out = spaceBeforePunct.ReplaceAllString(out, "$1")
	out = collapseRepeatedPunctuation(out)
	return strings.TrimSpace(out)
}

// collapseRepeatedPunctuation turns "!!!" into "!". RE2 has no backreferences,
// so the run is collapsed by hand.
func collapseRepeatedPunctuation(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))

	var previous rune
	first := true
	for _, r := range text {
		if !first && r == previous && isTrackedPunctuation(r) {
			continue
		}
		builder.WriteRune(r)
		previous = r
		first = false
	}
	return builder.String()
}

func isTrackedPunctuation(r rune) bool {
	switch r {
	case ',', '.', ';', ':', '!', '?':
		return true
	default:
		return false
	}
}
