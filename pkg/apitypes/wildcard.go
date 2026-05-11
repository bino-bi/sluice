// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"fmt"
	"regexp"
	"strings"
)

// Matcher tests whether a dotted path (e.g. "pg.public.orders") matches a
// compiled wildcard pattern.
type Matcher interface {
	Match(path string) bool
	Pattern() string
}

// CompileWildcard compiles a wildcard pattern into a Matcher.
//
// Syntax:
//
//   - "*"  matches zero or more characters within a single path segment
//   - "**" matches zero or more whole path segments (separated by ".")
//   - "\*" matches a literal asterisk
//
// Examples:
//
//   - "pg.*.orders"  matches "pg.public.orders" but not "pg.public.orders.archive"
//   - "pg.**"        matches any path starting with "pg."
//   - "analytics_*"  matches single segments starting with "analytics_"
func CompileWildcard(pattern string) (Matcher, error) {
	if pattern == "" {
		return nil, fmt.Errorf("apitypes: wildcard pattern is empty")
	}
	rx, err := wildcardToRegex(pattern)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile("^" + rx + "$")
	if err != nil {
		return nil, fmt.Errorf("apitypes: compile wildcard %q: %w", pattern, err)
	}
	return &regexMatcher{pat: pattern, re: re}, nil
}

type regexMatcher struct {
	pat string
	re  *regexp.Regexp
}

func (m *regexMatcher) Match(path string) bool { return m.re.MatchString(path) }
func (m *regexMatcher) Pattern() string        { return m.pat }

// wildcardToRegex converts our pattern syntax to an anchored regex body.
// It walks character by character so escapes can be handled precisely.
func wildcardToRegex(p string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '\\':
			if i+1 >= len(p) {
				return "", fmt.Errorf("apitypes: trailing backslash in wildcard %q", p)
			}
			next := p[i+1]
			b.WriteString(regexp.QuoteMeta(string(next)))
			i++
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				// ** matches zero or more whole segments. We model that as
				// ".*" (any char including dots).
				b.WriteString(".*")
				i++
			} else {
				// * matches within a single segment: any char except dot.
				b.WriteString("[^.]*")
			}
		case '.':
			b.WriteString(`\.`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String(), nil
}
