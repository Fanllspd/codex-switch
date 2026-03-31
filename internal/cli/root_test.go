package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/config"

	"github.com/spf13/cobra"
)

func TestCompletionCommandGeneratesZshScript(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		Now:            func() time.Time { return time.Unix(100, 0) },
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	if err := cmd.GenZshCompletion(&out); err != nil {
		t.Fatalf("GenZshCompletion: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("_codex-switch")) {
		t.Fatalf("expected zsh completion output, got %q", out.String())
	}
}

func TestHelpDoesNotNeedRuntimePreparation(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Codex Switch CLI")) {
		t.Fatalf("unexpected help output: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("[OPTIONS]")) {
		t.Fatalf("unexpected help output: %q", out.String())
	}
}

func TestSubcommandHelpUsesRequiredArgumentSyntax(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}

	cases := []struct {
		args   []string
		expect string
	}{
		{args: []string{"use", "-h"}, expect: "use <name>"},
		{args: []string{"rename", "-h"}, expect: "rename <old-name> <new-name>"},
		{args: []string{"delete", "-h"}, expect: "delete <name>"},
		{args: []string{"install-completion", "-h"}, expect: "install-completion <zsh|bash>"},
	}

	for _, tc := range cases {
		cmd := app.newRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute(%v): %v", tc.args, err)
		}
		if !bytes.Contains(out.Bytes(), []byte(tc.expect)) {
			t.Fatalf("expected help for %v to contain %q, got %q", tc.args, tc.expect, out.String())
		}
	}
}

func TestRootHelpShowsCommonCommandsFirst(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Client:         &http.Client{},
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	output := out.String()
	loginIndex := bytes.Index(out.Bytes(), []byte("\n  login"))
	listIndex := bytes.Index(out.Bytes(), []byte("\n  list"))
	completionIndex := bytes.Index(out.Bytes(), []byte("\n  completion"))
	if loginIndex == -1 || listIndex == -1 || completionIndex == -1 {
		t.Fatalf("missing expected commands in help output: %q", output)
	}
	if completionIndex < loginIndex || completionIndex < listIndex {
		t.Fatalf("expected common commands before completion, got %q", output)
	}
}

func TestCompleteAccountNamesStopsAfterFirstArg(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"work", "home"} {
		if err := os.WriteFile(filepath.Join(paths.AccountsDir, name+".json"), []byte(`{"tokens":{"account_id":"acct"}}`), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	completer := completeAccountNames(paths)

	firstArg, directive := completer(nil, nil, "wo")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("unexpected directive for first arg: %v", directive)
	}
	if len(firstArg) != 1 || firstArg[0] != "work" {
		t.Fatalf("unexpected first-arg completions: %v", firstArg)
	}

	secondArg, directive := completer(nil, []string{"work"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("unexpected directive for second arg: %v", directive)
	}
	if len(secondArg) != 0 {
		t.Fatalf("expected no second-arg completions, got %v", secondArg)
	}
}

func TestInstallCompletionWritesZshScript(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Client:         &http.Client{},
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install-completion", "zsh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	target := paths.HomeDir + "/.zsh/completions/_codex-switch"
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(content, []byte("_codex-switch")) {
		t.Fatalf("unexpected script content: %q", string(content))
	}
	if !bytes.Contains(out.Bytes(), []byte("Completion installed")) {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("shell: zsh")) {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestTokenInfoShowsRefreshTokenDetails(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	doc := `{"tokens":{"id_token":"header.payload.sig","access_token":"header.payload.sig","refresh_token":"opaque-refresh-token"}}`
	if err := os.WriteFile(paths.AuthFile, []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            func() time.Time { return time.Unix(100, 0) },
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"token-info"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !bytes.Contains(out.Bytes(), []byte("refresh_token")) {
		t.Fatalf("expected refresh_token row in output, got %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("refresh token type: opaque")) {
		t.Fatalf("expected refresh token type details, got %q", out.String())
	}
}

func TestDeletePromptsBeforeRemovingAccount(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(paths.AccountsDir, "work.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewBufferString("y\n"))
	cmd.SetArgs([]string{"delete", "work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected account to be removed, stat err=%v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`Delete saved account "work"?`)) {
		t.Fatalf("unexpected prompt output: %q", out.String())
	}
}

func TestPruneApplyPromptsBeforeDeletingDuplicates(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := []byte(`{"tokens":{"account_id":"acct-1","access_token":"a","id_token":"b"}}`)
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(paths.AccountsDir, name+".json"), content, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewBufferString("yes\n"))
	cmd.SetArgs([]string{"prune", "--apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(paths.AccountsDir, "*.json"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one account file after prune, got %d (%v)", len(files), files)
	}
	if !bytes.Contains(out.Bytes(), []byte("Delete 1 duplicate saved account(s)?")) {
		t.Fatalf("unexpected prompt output: %q", out.String())
	}
}

func TestPruneApplyStillShowsResultsWhenDeleteFails(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := []byte(`{"tokens":{"account_id":"acct-1","access_token":"a","id_token":"b"}}`)
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(paths.AccountsDir, name+".json"), content, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	if err := os.Chmod(paths.AccountsDir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer os.Chmod(paths.AccountsDir, 0o755)

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewBufferString("yes\n"))
	cmd.SetArgs([]string{"prune", "--apply"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected prune apply error")
	}
	if !bytes.Contains(out.Bytes(), []byte("Prune partially applied")) {
		t.Fatalf("expected partial-apply warning, got %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("KEEP")) || !bytes.Contains(out.Bytes(), []byte("REMOVE")) {
		t.Fatalf("expected prune result table, got %q", out.String())
	}
}

func TestDeleteWithoutNameFailsBeforePrompt(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"delete"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected missing arg error")
	}
	if bytes.Contains(out.Bytes(), []byte("Delete saved account")) {
		t.Fatalf("delete prompt should not appear when arg is missing: %q", out.String())
	}
}

func TestSaveWithoutNameFailsAtCLIArgumentValidation(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"save"})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected missing arg error")
	}
	if strings.Contains(err.Error(), "Please provide an account name.") {
		t.Fatalf("expected Cobra arg validation error, got business-layer error: %v", err)
	}
}

func TestPersistentPreRunReportsRefreshWarnings(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	doc := `{"tokens":{"account_id":"acct-1","access_token":"bad-access-token","refresh_token":""}}`
	if err := os.WriteFile(paths.AuthFile, []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: true,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"token-info"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Contains(errOut.Bytes(), []byte("startup refresh skipped")) {
		t.Fatalf("expected startup refresh warning, got stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestLoginReportsAliasSyncWarnings(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll accounts: %v", err)
	}
	target := filepath.Join(paths.AccountsDir, "work.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"account_id":"acct-1","access_token":"old","id_token":"old"}}`), 0o400); err != nil {
		t.Fatalf("WriteFile alias: %v", err)
	}

	loginScript := filepath.Join(paths.HomeDir, "fake-codex")
	script := "#!/bin/sh\n" +
		"mkdir -p '" + filepath.Dir(paths.AuthFile) + "'\n" +
		"cat > '" + paths.AuthFile + "' <<'EOF'\n" +
		"{\"tokens\":{\"account_id\":\"acct-1\",\"access_token\":\"new\",\"id_token\":\"new\"}}\n" +
		"EOF\n"
	if err := os.WriteFile(loginScript, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile script: %v", err)
	}
	cfg.CodexBin = loginScript

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"login"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("sync failed")) {
		t.Fatalf("expected login to surface sync warning, got %q", out.String())
	}
}

func TestSyncAllRecordsOnlySuccessfulChecks(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll accounts: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode refresh payload: %v", err)
			}
			switch payload["refresh_token"] {
			case "current-rt", "good-rt":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "refreshed-token",
					"id_token":      "refreshed-id",
					"refresh_token": payload["refresh_token"],
				})
			case "bad-rt":
				http.Error(w, "bad refresh", http.StatusBadRequest)
			default:
				http.Error(w, "unexpected refresh token", http.StatusBadRequest)
			}
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"email":     "test@example.com",
				"plan_type": "plus",
				"rate_limit": map[string]any{
					"allowed": true,
					"primary_window": map[string]any{
						"used_percent": 10,
						"reset_at":     float64(200),
					},
					"secondary_window": map[string]any{
						"used_percent": 30,
						"reset_at":     float64(400),
					},
				},
			})
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	cfg.Network.RefreshURL = server.URL
	cfg.Network.UsageURL = server.URL
	cfg.Network.RefreshClientID = "test-client"

	currentDoc := `{"tokens":{"account_id":"acct-current","access_token":"current-token","refresh_token":"current-rt"}}`
	if err := os.WriteFile(paths.AuthFile, []byte(currentDoc), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}

	goodDoc := `{"tokens":{"account_id":"acct-good","access_token":"good-token","refresh_token":"good-rt"}}`
	badDoc := `{"tokens":{"account_id":"acct-bad","access_token":"bad-token","refresh_token":"bad-rt"}}`
	if err := os.WriteFile(filepath.Join(paths.AccountsDir, "good.json"), []byte(goodDoc), 0o600); err != nil {
		t.Fatalf("WriteFile good alias: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.AccountsDir, "bad.json"), []byte(badDoc), 0o600); err != nil {
		t.Fatalf("WriteFile bad alias: %v", err)
	}

	app := &App{
		Paths:          paths,
		Client:         server.Client(),
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            func() time.Time { return time.Unix(100, 0) },
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"sync", "--all", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	metaBytes, err := os.ReadFile(paths.SyncMetaFile)
	if err != nil {
		t.Fatalf("ReadFile sync meta: %v", err)
	}
	if !bytes.Contains(metaBytes, []byte(`"good"`)) {
		t.Fatalf("expected successful alias to be recorded, got %q", string(metaBytes))
	}
	if bytes.Contains(metaBytes, []byte(`"bad"`)) {
		t.Fatalf("expected failed alias not to be recorded, got %q", string(metaBytes))
	}
}

func TestSyncReportsLastCheckedWriteWarnings(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll accounts: %v", err)
	}

	doc := `{"tokens":{"account_id":"acct-1","access_token":"a","id_token":"b"}}`
	if err := os.WriteFile(paths.AuthFile, []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.AccountsDir, "work.json"), []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile alias: %v", err)
	}
	if err := os.WriteFile(paths.SyncMetaFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile sync meta: %v", err)
	}
	if err := os.Chmod(paths.SyncMetaFile, 0o400); err != nil {
		t.Fatalf("Chmod sync meta: %v", err)
	}
	defer os.Chmod(paths.SyncMetaFile, 0o600)

	app := &App{
		Paths:          paths,
		Client:         &http.Client{},
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            func() time.Time { return time.Unix(100, 0) },
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"sync"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("last-checked update skipped")) {
		t.Fatalf("expected last-checked warning, got %q", out.String())
	}
}

func TestDeleteAcceptsPipedConfirmation(t *testing.T) {
	t.Parallel()

	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(paths.AccountsDir, "pipe.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := writer.WriteString("yes\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = writer.Close()
	defer reader.Close()

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(reader)
	cmd.SetArgs([]string{"delete", "pipe"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected account to be removed via piped confirmation, stat err=%v", err)
	}
}

func TestUseWithRelaunchPromptsBeforeRestartingApp(t *testing.T) {
	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll accounts dir: %v", err)
	}

	if err := os.WriteFile(paths.AuthFile, []byte(`{"tokens":{"account_id":"old"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.AccountsDir, "work.json"), []byte(`{"tokens":{"account_id":"new"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile alias: %v", err)
	}

	binDir := filepath.Join(paths.HomeDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin dir: %v", err)
	}
	logPath := filepath.Join(paths.HomeDir, "relaunch.log")
	script := "#!/bin/sh\n" +
		"printf '%s:%s\\n' \"$0\" \"$*\" >> '" + logPath + "'\n"
	for _, name := range []string{"osascript", "open"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewBufferString("yes\n"))
	cmd.SetArgs([]string{"use", "work", "--relaunch"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	authBytes, err := os.ReadFile(paths.AuthFile)
	if err != nil {
		t.Fatalf("ReadFile auth: %v", err)
	}
	if !bytes.Contains(authBytes, []byte(`"account_id":"new"`)) {
		t.Fatalf("expected auth.json to be switched, got %q", string(authBytes))
	}
	if !bytes.Contains(out.Bytes(), []byte("Relaunch Codex App now?")) {
		t.Fatalf("expected relaunch confirmation prompt, got %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Relaunching Codex App...")) {
		t.Fatalf("expected relaunch notice, got %q", out.String())
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile relaunch log: %v", err)
	}
	if !bytes.Contains(logBytes, []byte("/osascript:-e if application \"Codex\" is running then tell application \"Codex\" to quit")) {
		t.Fatalf("expected osascript quit command, got %q", string(logBytes))
	}
	if !bytes.Contains(logBytes, []byte("/open:-a Codex")) {
		t.Fatalf("expected open command, got %q", string(logBytes))
	}
}

func TestUseWithRelaunchSkipsRestartWhenNotConfirmed(t *testing.T) {
	paths := config.PathsFromHome(t.TempDir())
	cfg, err := config.Load(paths)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.MkdirAll(paths.AccountsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll accounts dir: %v", err)
	}

	if err := os.WriteFile(paths.AuthFile, []byte(`{"tokens":{"account_id":"old"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.AccountsDir, "work.json"), []byte(`{"tokens":{"account_id":"new"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile alias: %v", err)
	}

	binDir := filepath.Join(paths.HomeDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin dir: %v", err)
	}
	logPath := filepath.Join(paths.HomeDir, "relaunch.log")
	script := "#!/bin/sh\n" +
		"printf '%s:%s\\n' \"$0\" \"$*\" >> '" + logPath + "'\n"
	for _, name := range []string{"osascript", "open"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	app := &App{
		Paths:          paths,
		Config:         cfg,
		ConfigLoaded:   true,
		Now:            time.Now,
		PrepareRuntime: false,
	}
	cmd := app.newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewBufferString("n\n"))
	cmd.SetArgs([]string{"use", "work", "--relaunch"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	authBytes, err := os.ReadFile(paths.AuthFile)
	if err != nil {
		t.Fatalf("ReadFile auth: %v", err)
	}
	if !bytes.Contains(authBytes, []byte(`"account_id":"new"`)) {
		t.Fatalf("expected auth.json to be switched, got %q", string(authBytes))
	}
	if !bytes.Contains(out.Bytes(), []byte("Skipped Codex App relaunch")) {
		t.Fatalf("expected relaunch skip notice, got %q", out.String())
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("expected relaunch commands not to run, stat err=%v", err)
	}
}
