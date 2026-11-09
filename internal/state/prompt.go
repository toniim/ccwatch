package state

import (
	"regexp"
	"strings"
)

// PreviewRunes caps the preview of a prompt carried by a log row.
const PreviewRunes = 300

// Prompt kinds stored in Event.PromptKind.
const (
	KindOrcaTask         = "orca_task"
	KindPaste            = "paste"
	KindTaskNotification = "task_notification"
)

// OrcaTaskPrefix leads the log text of a prompt dispatched by an Orca coordinator.
const OrcaTaskPrefix = "[orca task] "

// OrcaNoBrief is shown for an Orca dispatch whose task text was not stored.
const OrcaNoBrief = "(brief not captured)"

const (
	orcaTaskMarker = "=== TASK ==="
	pasteClose     = "</pasted_content"
	orcaIntro      = "Please carry out this task from my Orca coordinator"
	orcaBoiler     = "You are working inside Orca, a multi-agent IDE"
)

var (
	pasteOpenRe = regexp.MustCompile(`<pasted_content[^>]*>`)
	summaryRe   = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	statusRe    = regexp.MustCompile(`(?s)<status>(.*?)</status>`)
)

// IsTaskNotification reports whether p is a background-task completion that
// Claude Code injected as a prompt.
func IsTaskNotification(p string) bool {
	return strings.HasPrefix(strings.TrimSpace(p), "<task-notification")
}

// NotificationParts returns the <summary> and <status> text of a task notification.
func NotificationParts(p string) (summary, status string) {
	if m := summaryRe.FindStringSubmatch(p); m != nil {
		summary = strings.TrimSpace(m[1])
	}
	if m := statusRe.FindStringSubmatch(p); m != nil {
		status = strings.TrimSpace(m[1])
	}
	return summary, status
}

// DisplayPrompt derives the text worth storing from a prompt and its kind:
// the task of an Orca dispatch (boilerplate dropped), the inner text of a
// pasted block, or the prompt itself (kind ""). An Orca dispatch without a
// TASK section yields kind orca_task and empty text.
func DisplayPrompt(p string) (text, kind string) {
	if i := strings.Index(p, orcaTaskMarker); i >= 0 {
		t := p[i+len(orcaTaskMarker):]
		if j := strings.LastIndex(t, pasteClose); j >= 0 {
			t = t[:j]
		}
		return strings.TrimSpace(t), KindOrcaTask
	}
	if strings.HasPrefix(p, orcaIntro) || strings.Contains(p, orcaBoiler) || strings.Contains(p, "=== CLI COMMANDS ===") {
		return "", KindOrcaTask
	}
	if loc := pasteOpenRe.FindStringIndex(p); loc != nil {
		t := p[loc[1]:]
		if j := strings.LastIndex(t, pasteClose); j >= 0 {
			t = t[:j]
		}
		return strings.TrimSpace(t), KindPaste
	}
	return p, ""
}

// PromptView derives, at read time, what a stored UserPromptSubmit shows:
// its kind, the display text and (task notifications) the status. Events of
// every age go through this one path; the stored prompt_kind is not trusted
// over the text.
func PromptView(e Event) (kind, text, status string) {
	if IsTaskNotification(e.Prompt) {
		text, status = NotificationParts(e.Prompt)
		return KindTaskNotification, text, status
	}
	text, kind = DisplayPrompt(e.Prompt)
	return kind, text, ""
}
