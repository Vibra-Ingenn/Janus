package validation

import (
	"fmt"
	"regexp"
	"strings"
)

// Banned patterns in AI-generated code/scripts.
// Any AI output containing these indicates an incomplete/hollow implementation.
var bannedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)#\s*TODO`),
	regexp.MustCompile(`(?i)//\s*TODO`),
	regexp.MustCompile(`(?m)^\s*pass\s*$`),         // Python bare 'pass' on its own line
	regexp.MustCompile(`(?i)NotImplementedError`),
	regexp.MustCompile(`--help\s*$`),                // command that only runs --help
	regexp.MustCompile(`--version\s*$`),             // command that only runs --version
}

// ValidationError records which pattern was found and a snippet of context.
type ValidationError struct {
	Pattern string
	Snippet string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("zero-trust: incomplete output detected — pattern %q found near: %q", e.Pattern, e.Snippet)
}

// ValidateContent scans text for banned placeholder/incomplete patterns.
// Returns nil if clean, or a ValidationError if a banned pattern is found.
func ValidateContent(content string) error {
	for _, re := range bannedPatterns {
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		// Get a short snippet around the match for context.
		start := loc[0]
		if start > 60 {
			start -= 60
		} else {
			start = 0
		}
		end := loc[1]
		if end+60 < len(content) {
			end += 60
		} else {
			end = len(content)
		}
		snippet := strings.TrimSpace(content[start:end])
		return &ValidationError{
			Pattern: re.String(),
			Snippet: snippet,
		}
	}
	return nil
}

// IsValidationError returns true if err is a ValidationError.
func IsValidationError(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}
