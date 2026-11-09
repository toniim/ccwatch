package view

import (
	"regexp"
	"strings"
)

var modelDate = regexp.MustCompile(`-\d{8}$`)

// ShortModel shortens a model id for display: it drops the "claude-" provider
// prefix and a trailing date stamp, and keeps a context suffix such as "[1m]".
// Values without the prefix (agent frontmatter aliases, "?", "") pass through.
func ShortModel(m string) string {
	suffix := ""
	if i := strings.Index(m, "["); i > 0 && strings.HasSuffix(m, "]") {
		m, suffix = m[:i], m[i:]
	}
	m = strings.TrimPrefix(m, "claude-")
	m = modelDate.ReplaceAllString(m, "")
	return m + suffix
}
