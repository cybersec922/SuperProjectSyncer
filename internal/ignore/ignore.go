package ignore

import (
	"path/filepath"
	"strings"
)

// Matcher evaluates glob patterns similar to .gitignore.
type Matcher struct {
	patterns []pattern
}

type pattern struct {
	negate   bool
	segments []string
	dirOnly  bool
}

func New(patterns []string) *Matcher {
	m := &Matcher{}
	for _, p := range patterns {
		m.patterns = append(m.patterns, parsePattern(p))
	}
	return m
}

func parsePattern(raw string) pattern {
	p := pattern{}
	s := strings.TrimSpace(raw)
	if s == "" {
		return p
	}
	if strings.HasPrefix(s, "!") {
		p.negate = true
		s = s[1:]
	}
	if strings.HasSuffix(s, "/") {
		p.dirOnly = true
		s = strings.TrimSuffix(s, "/")
	}
	s = filepath.ToSlash(s)
	if strings.Contains(s, "/") {
		p.segments = strings.Split(s, "/")
	} else {
		p.segments = []string{s}
	}
	return p
}

// Ignored reports whether relPath (relative to sync root, slash-separated) should be skipped.
func (m *Matcher) Ignored(relPath string) bool {
	if relPath == "" {
		return false
	}
	relPath = filepath.ToSlash(relPath)
	ignored := false
	for _, pat := range m.patterns {
		if pat.match(relPath) {
			if pat.negate {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

func (p pattern) match(relPath string) bool {
	if len(p.segments) == 1 && !strings.Contains(p.segments[0], "**") {
		seg := p.segments[0]
		base := filepath.Base(relPath)
		if p.dirOnly {
			return relPath == seg || strings.HasPrefix(relPath, seg+"/")
		}
		return matchGlob(seg, base) || matchGlob(seg, relPath)
	}
	return matchPathPattern(p.segments, relPath, p.dirOnly)
}

func matchPathPattern(segments []string, relPath string, dirOnly bool) bool {
	parts := strings.Split(relPath, "/")
	if dirOnly && len(parts) > 0 {
		// directory pattern: match prefix
		for i := range parts {
			sub := strings.Join(parts[:i+1], "/")
			if matchSegments(segments, sub, parts[:i+1]) {
				return true
			}
		}
		return false
	}
	return matchSegments(segments, relPath, parts)
}

func matchSegments(segments []string, full string, parts []string) bool {
	if len(segments) == 0 {
		return false
	}
	// ** anywhere
	joined := strings.Join(segments, "/")
	if joined == "**" {
		return true
	}
	if strings.HasPrefix(joined, "**/") {
		suffix := strings.TrimPrefix(joined, "**/")
		return strings.HasSuffix(full, suffix) || matchGlob(suffix, filepath.Base(full))
	}
	if strings.HasSuffix(joined, "/**") {
		prefix := strings.TrimSuffix(joined, "/**")
		if full == prefix || strings.HasPrefix(full, prefix+"/") {
			return true
		}
		// match prefix as any path segment (e.g. src/node_modules/x)
		for _, part := range parts {
			if part == prefix {
				return true
			}
		}
		return strings.Contains(full, "/"+prefix+"/")
	}
	if len(segments) != len(parts) {
		// allow ** in middle
		return matchSegmentsFlexible(segments, parts)
	}
	for i, seg := range segments {
		if seg == "**" {
			return true
		}
		if !matchGlob(seg, parts[i]) {
			return false
		}
	}
	return true
}

func matchSegmentsFlexible(segments, parts []string) bool {
	si, pi := 0, 0
	for si < len(segments) && pi < len(parts) {
		if segments[si] == "**" {
			if si == len(segments)-1 {
				return true
			}
			for pi <= len(parts) {
				if matchSegmentsFlexible(segments[si+1:], parts[pi:]) {
					return true
				}
				pi++
			}
			return false
		}
		if !matchGlob(segments[si], parts[pi]) {
			return false
		}
		si++
		pi++
	}
	for si < len(segments) && segments[si] == "**" {
		si++
	}
	return si == len(segments) && pi == len(parts)
}

func matchGlob(pattern, name string) bool {
	ok, _ := filepath.Match(pattern, name)
	return ok
}
