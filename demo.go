package main

import (
	"encoding/json"
	"strings"
)

var demoEvents = []string{"SessionStart", "UserPromptSubmit", "SubagentStart", "SubagentStop", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"}

// demoSettings returns the settings JSON for `claude --settings`.
func demoSettings(exe string) string {
	hooks := map[string]any{}
	for _, ev := range demoEvents {
		hooks[ev] = []any{map[string]any{
			"matcher": "*",
			"hooks":   []any{map[string]any{"type": "command", "command": exe + " hook", "async": true}},
		}}
	}
	b, _ := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	return string(b) + "\n"
}

// demoEnv returns the export lines for the OTel environment.
func demoEnv() string {
	return strings.Join([]string{
		"export CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"export OTEL_LOGS_EXPORTER=otlp",
		"export OTEL_METRICS_EXPORTER=otlp",
		"export OTEL_EXPORTER_OTLP_PROTOCOL=http/json",
		"export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4319",
		"export OTEL_LOGS_EXPORT_INTERVAL=1000",
		"",
	}, "\n")
}
