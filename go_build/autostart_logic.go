package main

import "strings"

const autoStartFlag = "--autostart"

func buildAutoStartCommand(exePath string) string {
	cleanPath := strings.ReplaceAll(strings.TrimSpace(exePath), `"`, "")
	return `"` + cleanPath + `" ` + autoStartFlag
}

func isExpectedAutoStartCommand(command, exePath string) bool {
	return strings.EqualFold(strings.TrimSpace(command), buildAutoStartCommand(exePath))
}

func isLegacyAutoStartCommand(command, exePath string) bool {
	legacy := `"` + strings.ReplaceAll(strings.TrimSpace(exePath), `"`, "") + `"`
	return strings.EqualFold(strings.TrimSpace(command), legacy)
}
