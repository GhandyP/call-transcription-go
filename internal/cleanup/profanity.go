package cleanup

import (
	"regexp"
	"strings"
)

// profanityTerms is a deliberately small, explicit word list. It is meant to be
// extended by the call owner, not to be complete: the policy exists so a call
// is flagged for a human, and a word list can never be exhaustive.
var profanityTerms = []string{
	"asshole",
	"bastard",
	"bitch",
	"crap",
	"damn",
	"dick",
	"fuck",
	"hell",
	"piss",
	"shit",
}

// profanityPattern matches a listed term as a whole word, with the usual
// inflections. Matching whole words matters in both directions: "shit" matches
// "shit" and "shitting", and it does not match "shitake", which is a word that
// merely starts with the same letters. RE2 has no backreferences, so the
// doubled-consonant inflections are listed explicitly.
var profanityPattern = buildProfanityPattern()

// buildProfanityPattern assembles one alternation of whole-word variants.
func buildProfanityPattern() *regexp.Regexp {
	variants := make([]string, 0, len(profanityTerms)*11)
	seen := make(map[string]struct{})

	for _, term := range profanityTerms {
		if term == "" {
			continue
		}
		last := string(term[len(term)-1])
		for _, variant := range []string{
			term,
			term + "s",
			term + "es",
			term + "ed",
			term + "ing",
			term + last + "ed",
			term + last + "ing",
			term + "er",
			term + "ers",
			term + "y",
			term + "ies",
		} {
			if _, ok := seen[variant]; ok {
				continue
			}
			seen[variant] = struct{}{}
			variants = append(variants, regexp.QuoteMeta(variant))
		}
	}

	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(variants, "|") + `)\b`)
}

// matchesProfanity reports whether the text contains a listed term.
func matchesProfanity(text string) bool {
	return profanityPattern.MatchString(text)
}

// matchedProfanity is used to count matches for the finding that is written to
// the caller. The matched words are never included in that finding.
func matchedProfanity(text string) []string {
	return profanityPattern.FindAllString(text, -1)
}

// redactProfanity replaces every matched term with PlaceholderProfanity.
func redactProfanity(text string) string {
	return profanityPattern.ReplaceAllString(text, PlaceholderProfanity)
}
