package cli

import (
	"fmt"
	"time"
)

const codexAppName = "Codex"

func relaunchCodexApp() error {
	if _, err := execLookPath("osascript"); err != nil {
		return fmt.Errorf("unable to relaunch Codex App: osascript not found in PATH")
	}
	if _, err := execLookPath("open"); err != nil {
		return fmt.Errorf("unable to relaunch Codex App: open not found in PATH")
	}

	quitCmd := execCommand(
		"osascript",
		"-e",
		fmt.Sprintf(`if application "%s" is running then tell application "%s" to quit`, codexAppName, codexAppName),
	)
	if err := quitCmd.Run(); err != nil {
		return fmt.Errorf("quit Codex App: %w", err)
	}

	time.Sleep(1200 * time.Millisecond)

	openCmd := execCommand("open", "-a", codexAppName)
	if err := openCmd.Run(); err != nil {
		return fmt.Errorf("open Codex App: %w", err)
	}
	return nil
}
