package cleanup

import (
	"math"
	"regexp"
)

// redactionRule is one deterministic pattern: it matches a class of sensitive
// value and says what replaces it. The replacement template may reference a
// capture group, so a rule can keep the word that introduces the value (for
// example "Bearer") while removing the value itself.
type redactionRule struct {
	name    string
	pattern *regexp.Regexp
	// replace is a Go regexp replacement template. A template that references
	// no group is the placeholder itself.
	replace string
}

// redactionRules are applied in order, each one on the text the previous ones
// produced, so the result of a run is always the same.
var redactionRules = []redactionRule{
	{
		name:    "email",
		pattern: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
		replace: PlaceholderEmail,
	},
	{
		name:    "private_key",
		pattern: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`),
		replace: PlaceholderPrivateKey,
	},
	{
		name:    "bearer_token",
		pattern: regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=\-]{8,}`),
		replace: "${1}" + PlaceholderToken,
	},
	{
		name:    "api_key",
		pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{8,}\b`),
		replace: PlaceholderAPIKey,
	},
	{
		name:    "api_key",
		pattern: regexp.MustCompile(`\bAKIA[0-9A-Z]{12,}\b`),
		replace: PlaceholderAPIKey,
	},
	{
		name:    "api_key",
		pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{20,}\b`),
		replace: PlaceholderAPIKey,
	},
	{
		name:    "ssn",
		pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		replace: PlaceholderSSN,
	},
	{
		name:    "card",
		pattern: regexp.MustCompile(`\b\d{4}(?:[ -]\d{4}){3}\b`),
		replace: PlaceholderCard,
	},
	{
		name:    "card",
		pattern: regexp.MustCompile(`\b\d{13,19}\b`),
		replace: PlaceholderCard,
	},
}

// redactSensitive applies every rule and then the entropy rule, returning the
// rewritten text and the names of the rules that matched at least once, in
// order and without duplicates. It never returns the matched values.
func redactSensitive(text string) (string, []string) {
	var matched []string
	seen := make(map[string]struct{})

	record := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		matched = append(matched, name)
	}

	for _, rule := range redactionRules {
		if !rule.pattern.MatchString(text) {
			continue
		}
		text = rule.pattern.ReplaceAllString(text, rule.replace)
		record(rule.name)
	}

	if rewritten, found := redactHighEntropyTokens(text); found {
		text = rewritten
		record("high_entropy_token")
	}

	return text, matched
}

// candidateRun matches a long run of characters that a generated credential is
// usually made of. Whether such a run really looks like a secret is decided in
// code, because RE2 cannot express "mixed case and digits with high entropy".
var candidateRun = regexp.MustCompile(`[A-Za-z0-9_+/=\-]{24,}`)

// redactHighEntropyTokens replaces long mixed alphanumeric runs whose character
// distribution looks random. A long ordinary word has no digits and is left
// alone; a long run that mixes letters and digits with a high entropy is
// treated as a credential.
func redactHighEntropyTokens(text string) (string, bool) {
	found := false

	rewritten := candidateRun.ReplaceAllStringFunc(text, func(run string) string {
		if !looksRandom(run) {
			return run
		}
		found = true
		return PlaceholderToken
	})

	return rewritten, found
}

// looksRandom reports whether a run is credential-shaped: at least 24
// characters, mixing letters and digits, with an entropy of at least 3.5 bits
// per character.
func looksRandom(run string) bool {
	if len(run) < 24 {
		return false
	}

	var hasDigit, hasLetter bool
	counts := make(map[rune]int, len(run))
	for _, r := range run {
		counts[r]++
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	if !hasDigit || !hasLetter {
		return false
	}

	total := float64(len(run))
	entropy := 0.0
	for _, count := range counts {
		p := float64(count) / total
		entropy -= p * math.Log2(p)
	}
	return entropy >= 3.5
}
