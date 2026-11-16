// Package glob provides the wildcard matching used by scope, intercept and
// record rules. Patterns support * and ? and are matched against the full URL
// as well as host+path, so both "*.example.com" and "https://example.com/*"
// behave sensibly.
package glob

import (
	"net/url"
	"regexp"
	"strings"
)

type Matcher struct{ re *regexp.Regexp }

func Compile(patterns []string) []Matcher {
	out := make([]Matcher, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		var b strings.Builder
		b.WriteString("(?i)^")
		for _, r := range p {
			switch r {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err == nil {
			out = append(out, Matcher{re: re})
		}
	}
	return out
}

func Match(ms []Matcher, rawURL string) bool {
	if len(ms) == 0 {
		return false
	}
	alt := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		alt = u.Host + u.Path
	}
	for _, m := range ms {
		if m.re.MatchString(rawURL) || m.re.MatchString(alt) {
			return true
		}
	}
	return false
}

func Any(patterns []string, rawURL string) bool {
	return Match(Compile(patterns), rawURL)
}
