package cli

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"codex-switch/internal/accounts"
	"codex-switch/internal/auth"
	"codex-switch/internal/config"
	"codex-switch/internal/support"
	"codex-switch/internal/usage"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type App struct {
	Paths          config.Paths
	Config         config.Config
	ConfigLoaded   bool
	Client         *http.Client
	Now            func() time.Time
	PrepareRuntime bool
}

func NewRootCmd() (*cobra.Command, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}

	app := &App{
		Paths:          paths,
		Client:         &http.Client{},
		Now:            time.Now,
		PrepareRuntime: true,
	}

	return app.newRootCmd(), nil
}

func (a *App) newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "codex-switch",
		Short:         "Codex Account Switcher",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !a.PrepareRuntime || shouldSkipRuntimePreparation(cmd) {
				return nil
			}
			if err := a.ensureConfig(); err != nil {
				return err
			}
			now := a.Now()
			if _, err := os.Stat(a.Paths.AuthFile); err == nil {
				if _, err := auth.RefreshAuthFileIfNeeded(a.Client, a.Config, a.Paths.AuthFile, false, now); err != nil {
					printInfoWarning(cmd.ErrOrStderr(), fmt.Sprintf("startup refresh skipped: %v", err))
				}
			}
			_, checked, warnings := accounts.SyncSavedAliases(a.Paths)
			for _, warning := range filterStartupWarnings(warnings) {
				printInfoWarning(cmd.ErrOrStderr(), warning)
			}
			if len(checked) > 0 {
				if err := accounts.RecordLastChecked(a.Paths, checked, now); err != nil {
					printInfoWarning(cmd.ErrOrStderr(), fmt.Sprintf("last-checked update skipped: %v", err))
				}
			}
			return nil
		},
	}

	rootCmd.AddCommand(a.newLoginCmd())
	rootCmd.AddCommand(a.newTokenInfoCmd())
	rootCmd.AddCommand(a.newSaveCmd())
	rootCmd.AddCommand(a.newUseCmd())
	rootCmd.AddCommand(a.newListCmd())
	rootCmd.AddCommand(a.newCurrentCmd())
	rootCmd.AddCommand(a.newSyncCmd())
	rootCmd.AddCommand(a.newRenameCmd())
	rootCmd.AddCommand(a.newDoctorCmd())
	rootCmd.AddCommand(a.newPruneCmd())
	rootCmd.AddCommand(a.newDeleteCmd())
	rootCmd.AddCommand(a.newInstallCompletionCmd())

	rootCmd.InitDefaultCompletionCmd()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		renderHelp(cmd.OutOrStdout(), cmd)
	})
	rootCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		renderHelp(cmd.OutOrStdout(), cmd)
		return nil
	})
	return rootCmd
}

func (a *App) newLoginCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "login [name]",
		Short: "Run `codex login` and optionally save it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return a.runLogin(cmd, name, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing saved alias")
	return cmd
}

func (a *App) newTokenInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token-info",
		Short: "Show token timestamps and refresh state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runTokenInfo(cmd)
		},
	}
}

func (a *App) newSaveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "save <name>",
		Short:             "Save the current account",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeAccountNames(a.Paths),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := accounts.Save(a.Paths, name, force); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Saved %s", name)))
			fmt.Fprintln(cmd.OutOrStdout())
			return a.showNamedRows(cmd, []string{name}, false)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing saved alias")
	return cmd
}

func (a *App) newUseCmd() *cobra.Command {
	var relaunch bool
	cmd := &cobra.Command{
		Use:               "use <name>",
		Short:             "Switch to a saved account",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeAccountNames(a.Paths),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := accounts.Use(a.Paths, name); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Switched to %s", name)))
			fmt.Fprintln(cmd.OutOrStdout())
			if !relaunch {
				return a.runCurrent(cmd)
			}

			confirmed, err := confirmOptionalAction(cmd, "Relaunch Codex App now?")
			if err != nil {
				return err
			}
			if !confirmed {
				printInfoWarning(cmd.OutOrStdout(), "Skipped Codex App relaunch. Restart the app manually to apply the new account.")
				fmt.Fprintln(cmd.OutOrStdout())
				return a.runCurrent(cmd)
			}

			fmt.Fprintln(cmd.OutOrStdout(), colorize("Relaunching Codex App..."))
			return relaunchCodexApp()
		},
	}
	cmd.Flags().BoolVar(&relaunch, "relaunch", false, "Prompt to relaunch Codex App after switching")
	return cmd
}

func (a *App) newListCmd() *cobra.Command {
	var localOnly bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runList(cmd, localOnly)
		},
	}
	cmd.Flags().BoolVar(&localOnly, "local", false, "Skip live usage lookups")
	return cmd
}

func (a *App) newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show the current account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runCurrent(cmd)
		},
	}
}

func (a *App) newSyncCmd() *cobra.Command {
	var force bool
	var syncAll bool
	var currentOnly bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Refresh near-expiry tokens and sync aliases",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if syncAll && currentOnly {
				return fmt.Errorf("use either --current or --all, not both")
			}
			scope := "current"
			if syncAll {
				scope = "all"
			}
			return a.runSync(cmd, scope, force)
		},
	}
	cmd.Flags().BoolVar(&currentOnly, "current", false, "Sync only the current account")
	cmd.Flags().BoolVar(&syncAll, "all", false, "Sync every saved account")
	cmd.Flags().BoolVar(&force, "force", false, "Force a refresh even if tokens are not near expiry")
	return cmd
}

func (a *App) newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "rename <old-name> <new-name>",
		Short:             "Rename a saved account",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeRenameArgs(a.Paths),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName := args[0]
			newName := args[1]
			if err := accounts.Rename(a.Paths, oldName, newName); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Renamed %s -> %s", oldName, newName)))
			fmt.Fprintln(cmd.OutOrStdout())
			return a.showNamedRows(cmd, []string{newName}, false)
		},
	}
}

func (a *App) newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check auth and saved accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runDoctor(cmd)
		},
	}
}

func (a *App) newPruneCmd() *cobra.Command {
	var apply bool
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Preview or remove duplicate accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runPrune(cmd, apply, assumeYes)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Delete duplicate aliases")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func (a *App) newDeleteCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:               "delete <name>",
		Short:             "Delete an account",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeAccountNames(a.Paths),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := confirmAction(cmd, assumeYes, fmt.Sprintf("Delete saved account %q?", name)); err != nil {
				return err
			}
			if err := accounts.Delete(a.Paths, name); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Deleted %s", name)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func (a *App) newInstallCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "install-completion <zsh|bash>",
		Short:             "Install shell completion for the current user",
		Args:              cobra.ExactArgs(1),
		ValidArgs:         []string{"zsh", "bash"},
		ValidArgsFunction: cobra.FixedCompletions([]string{"zsh", "bash"}, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runInstallCompletion(cmd, args[0])
		},
	}
}

func (a *App) runLogin(cmd *cobra.Command, name string, force bool) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	codexBin := resolveCodexBin(a.Config)
	if codexBin == "" {
		return fmt.Errorf("unable to find `codex` in PATH")
	}

	loginCmd := execCommand(codexBin, "login")
	loginCmd.Stdout = cmd.OutOrStdout()
	loginCmd.Stderr = cmd.ErrOrStderr()
	loginCmd.Stdin = os.Stdin
	if err := loginCmd.Run(); err != nil {
		return err
	}

	if _, err := os.Stat(a.Paths.AuthFile); err != nil {
		return fmt.Errorf("login completed, but auth.json was not created")
	}

	updated, checked, warnings := accounts.SyncSavedAliases(a.Paths)
	if len(checked) > 0 {
		if err := accounts.RecordLastChecked(a.Paths, checked, a.Now()); err != nil {
			printInfoWarning(cmd.OutOrStdout(), fmt.Sprintf("last-checked update skipped: %v", err))
			fmt.Fprintln(cmd.OutOrStdout())
		}
	}
	if len(updated) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Synced aliases: %s", strings.Join(updated, ", "))))
		fmt.Fprintln(cmd.OutOrStdout())
	}
	for _, warning := range warnings {
		printInfoWarning(cmd.OutOrStdout(), warning)
	}
	if len(warnings) > 0 {
		fmt.Fprintln(cmd.OutOrStdout())
	}

	if name != "" {
		if err := accounts.Save(a.Paths, name, force); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Saved %s", name)))
		fmt.Fprintln(cmd.OutOrStdout())
		return a.showNamedRows(cmd, []string{name}, false)
	}

	return a.runCurrent(cmd)
}

func (a *App) runTokenInfo(cmd *cobra.Command) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	doc, err := auth.LoadDocument(a.Paths.AuthFile)
	if err != nil {
		if os.IsNotExist(err) {
			printInfo(cmd.OutOrStdout(), "Not logged in.")
			return nil
		}
		return err
	}

	tokens := auth.Tokens(doc)
	now := a.Now()
	rows := [][]string{
		{"id_token", present(tokens["id_token"]), support.FormatUnix(auth.IssuedAtUnix(tokens["id_token"]), now), support.FormatUnix(auth.ExpirationUnix(tokens["id_token"]), now), lifetime(tokens["id_token"])},
		{"access_token", present(tokens["access_token"]), support.FormatUnix(auth.IssuedAtUnix(tokens["access_token"]), now), support.FormatUnix(auth.ExpirationUnix(tokens["access_token"]), now), lifetime(tokens["access_token"])},
		{"refresh_token", present(tokens["refresh_token"]), "-", "-", refreshTokenDetails(tokens["refresh_token"])},
	}

	printHeadline(cmd.OutOrStdout(), "Token info")
	printTable(cmd.OutOrStdout(), []string{"TOKEN", "PRESENT", "ISSUED", "EXPIRES", "LIFETIME"}, rows)
	fmt.Fprintln(cmd.OutOrStdout())
	printKeyValue(cmd.OutOrStdout(), "config file", a.Paths.ConfigFile)
	printKeyValue(cmd.OutOrStdout(), "refresh margin", a.Config.Refresh.Margin)
	printKeyValue(cmd.OutOrStdout(), "usage timeout", a.Config.UsageTimeoutDuration().String())
	printKeyValue(cmd.OutOrStdout(), "max usage workers", fmt.Sprintf("%d", a.Config.Network.MaxUsageWorkers))
	printKeyValue(cmd.OutOrStdout(), "refresh timeout", a.Config.RefreshTimeoutDuration().String())
	printKeyValue(cmd.OutOrStdout(), "wham usage url", a.Config.Network.UsageURL)
	printKeyValue(cmd.OutOrStdout(), "refresh url", a.Config.Network.RefreshURL)
	printKeyValue(cmd.OutOrStdout(), "refresh client id", fallback(auth.ResolveRefreshClientID(tokens, a.Config), "-"))
	printKeyValue(cmd.OutOrStdout(), "codex bin", fallback(resolveCodexBin(a.Config), "-"))
	printKeyValue(cmd.OutOrStdout(), "refresh token", present(tokens["refresh_token"]))
	printKeyValue(cmd.OutOrStdout(), "refresh token type", refreshTokenDetails(tokens["refresh_token"]))
	printKeyValue(cmd.OutOrStdout(), "access token needs refresh", yesNo(auth.ShouldRefreshAccessToken(tokens["access_token"], a.Config.RefreshMarginDuration(), now)))
	printKeyValue(cmd.OutOrStdout(), "last refresh", support.FormatISO8601(stringValue(doc["last_refresh"])))
	return nil
}

func (a *App) runList(cmd *cobra.Command, localOnly bool) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	if len(accounts.ListAccountNames(a.Paths)) == 0 {
		printInfo(cmd.OutOrStdout(), "No saved accounts.")
		return nil
	}
	rows := accounts.BuildListRows(a.Paths, a.Config, a.Client, localOnly, a.Now())
	printListRows(cmd, rows)
	return nil
}

func (a *App) runCurrent(cmd *cobra.Command) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	if _, err := os.Stat(a.Paths.AuthFile); err != nil {
		printInfo(cmd.OutOrStdout(), "Not logged in.")
		return nil
	}

	rows := accounts.BuildListRows(a.Paths, a.Config, a.Client, false, a.Now())
	filtered := filterCurrentRows(rows)
	if len(filtered.Rows) == 0 {
		printInfo(cmd.OutOrStdout(), "Current account is unnamed (not saved).")
		return nil
	}
	printListRows(cmd, filtered)
	return nil
}

func (a *App) runSync(cmd *cobra.Command, scope string, force bool) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	now := a.Now()
	refreshedNames := []string{}
	refreshNotes := []string{}
	checkedNames := []string{}

	if scope == "all" {
		if refreshed, err := auth.RefreshAuthFileIfNeeded(a.Client, a.Config, a.Paths.AuthFile, force, now); err == nil && refreshed {
			refreshedNames = append(refreshedNames, "current")
		} else if err != nil {
			refreshNotes = append(refreshNotes, err.Error())
		}

		currentAlias := accounts.DetectCurrentAccountName(a.Paths)
		currentID := accounts.AccountIDFromFile(a.Paths.AuthFile)
		for _, name := range accounts.ListAccountNames(a.Paths) {
			path := filepath.Join(a.Paths.AccountsDir, name+".json")
			if name == currentAlias {
				continue
			}
			if currentID != "" && accounts.AccountIDFromFile(path) == currentID {
				continue
			}
			refreshed, err := auth.RefreshAuthFileIfNeeded(a.Client, a.Config, path, force, now)
			if err != nil {
				refreshNotes = append(refreshNotes, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			checkedNames = append(checkedNames, name)
			if refreshed {
				refreshedNames = append(refreshedNames, name)
			}
		}

		updated, checked, warnings := accounts.SyncSavedAliases(a.Paths)
		checkedNames = append(checkedNames, checked...)
		if err := printWarningsOrSummary(cmd, warnings, refreshNotes, refreshedNames, updated, force); err != nil {
			return err
		}
		if err := accounts.RecordLastChecked(a.Paths, checkedNames, now); err != nil {
			printInfoWarning(cmd.OutOrStdout(), fmt.Sprintf("last-checked update skipped: %v", err))
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return a.runCurrent(cmd)
	}

	refreshed, err := auth.RefreshAuthFileIfNeeded(a.Client, a.Config, a.Paths.AuthFile, force, now)
	if err != nil {
		refreshNotes = append(refreshNotes, err.Error())
	} else if refreshed {
		refreshedNames = append(refreshedNames, "current")
	}
	updated, checked, warnings := accounts.SyncSavedAliases(a.Paths)
	checkedNames = append(checkedNames, checked...)
	if err := printWarningsOrSummary(cmd, warnings, refreshNotes, refreshedNames, updated, force); err != nil {
		return err
	}
	if err := accounts.RecordLastChecked(a.Paths, checkedNames, now); err != nil {
		printInfoWarning(cmd.OutOrStdout(), fmt.Sprintf("last-checked update skipped: %v", err))
		fmt.Fprintln(cmd.OutOrStdout())
	}
	return a.runCurrent(cmd)
}

func (a *App) runDoctor(cmd *cobra.Command) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	files := accounts.ListAccountNames(a.Paths)
	currentName := accounts.DetectCurrentAccountName(a.Paths)
	rows := [][]string{
		{"codex dir", status(dirExists(a.Paths.CodexDir)), a.Paths.CodexDir},
		{"auth.json", status(fileExists(a.Paths.AuthFile)), a.Paths.AuthFile},
		{"accounts", status(dirExists(a.Paths.AccountsDir)), a.Paths.AccountsDir},
		{"saved accounts", boolStatus(len(files) > 0, "ok", "empty"), fmt.Sprintf("%d", len(files))},
		{"current alias", boolStatus(currentName != "", "ok", "unknown"), fallback(currentName, "-")},
	}

	printHeadline(cmd.OutOrStdout(), "Doctor")
	printTable(cmd.OutOrStdout(), []string{"CHECK", "STATUS", "DETAIL"}, rows)
	if len(files) == 0 {
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout())
	detailRows := [][]string{}
	notes := []string{}
	for _, name := range files {
		path := filepath.Join(a.Paths.AccountsDir, name+".json")
		snapshot, err := accounts.ReadSnapshot(path, a.Now())
		if err != nil {
			detailRows = append(detailRows, []string{name, "bad", "no", "no", "read failed"})
			notes = append(notes, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		liveUsage, usageErr := usage.Fetch(a.Client, a.Config, snapshot.Tokens["access_token"])
		live := "fail"
		if usageErr == nil && liveUsage != nil {
			live = "ok"
		}
		detailRows = append(detailRows, []string{name, "ok", yesNo(snapshot.AccountID != ""), yesNo(snapshot.Tokens["access_token"] != ""), live})
		if usageErr != nil {
			notes = append(notes, fmt.Sprintf("%s: live usage check failed: %v", name, usageErr))
		}
	}

	printTable(cmd.OutOrStdout(), []string{"ACCOUNT", "JSON", "ACCOUNT ID", "ACCESS TOKEN", "LIVE USAGE"}, detailRows)
	printNotes(cmd, notes)
	return nil
}

func (a *App) runPrune(cmd *cobra.Command, apply bool, assumeYes bool) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	pairs, err := accounts.Prune(a.Paths, false)
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		printInfo(cmd.OutOrStdout(), "No duplicates found.")
		return nil
	}

	if apply {
		pruneErr := error(nil)
		if err := confirmAction(
			cmd,
			assumeYes,
			fmt.Sprintf("Delete %d duplicate saved account(s)?", len(pairs)),
		); err != nil {
			return err
		}
		pairs, pruneErr = accounts.Prune(a.Paths, true)
		if pruneErr == nil {
			fmt.Fprintln(cmd.OutOrStdout(), colorize("Prune applied"))
		} else {
			printInfoWarning(cmd.OutOrStdout(), "Prune partially applied")
		}
		fmt.Fprintln(cmd.OutOrStdout())
		rows := make([][]string, 0, len(pairs))
		for _, pair := range pairs {
			rows = append(rows, []string{pair.Keep, pair.Remove})
		}
		header := []string{"KEEP", "REMOVED"}
		if pruneErr != nil {
			header = []string{"KEEP", "REMOVE"}
		}
		printTable(cmd.OutOrStdout(), header, rows)
		if pruneErr != nil {
			return pruneErr
		}
		return nil
	}

	printHeadline(cmd.OutOrStdout(), "Prune preview")
	rows := make([][]string, 0, len(pairs))
	for _, pair := range pairs {
		rows = append(rows, []string{pair.Keep, pair.Remove})
	}
	printTable(cmd.OutOrStdout(), []string{"KEEP", "REMOVE"}, rows)
	fmt.Fprintln(cmd.OutOrStdout())
	printCommand(cmd.OutOrStdout(), "Run `prune --apply` to remove the duplicate aliases above.")
	return nil
}

func (a *App) runInstallCompletion(cmd *cobra.Command, shell string) error {
	paths := a.Paths
	root := cmd.Root()
	switch shell {
	case "zsh":
		dir := filepath.Join(paths.HomeDir, ".zsh", "completions")
		target := filepath.Join(dir, "_codex-switch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		file, err := os.Create(target)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := root.GenZshCompletion(file); err != nil {
			return err
		}
		printHeadline(cmd.OutOrStdout(), "Completion installed")
		printKeyValue(cmd.OutOrStdout(), "shell", "zsh")
		printKeyValue(cmd.OutOrStdout(), "path", target)
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle("Run these if your ~/.zshrc does not already configure zsh completions:", ansiFeatureLabelStyle))
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle(`echo 'fpath=(~/.zsh/completions $fpath)' >> ~/.zshrc`, ansiFeatureCommandStyle))
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle(`echo 'autoload -U compinit && compinit' >> ~/.zshrc`, ansiFeatureCommandStyle))
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle(`source ~/.zshrc`, ansiFeatureCommandStyle))
		return nil
	case "bash":
		dir := filepath.Join(paths.HomeDir, ".local", "share", "bash-completion", "completions")
		target := filepath.Join(dir, "codex-switch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		file, err := os.Create(target)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := root.GenBashCompletionV2(file, true); err != nil {
			return err
		}
		printHeadline(cmd.OutOrStdout(), "Completion installed")
		printKeyValue(cmd.OutOrStdout(), "shell", "bash")
		printKeyValue(cmd.OutOrStdout(), "path", target)
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle("Run this to reload your shell config:", ansiFeatureLabelStyle))
		fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle(`source ~/.bashrc`, ansiFeatureCommandStyle))
		return nil
	default:
		return fmt.Errorf("unsupported shell %q", shell)
	}
}

func (a *App) showNamedRows(cmd *cobra.Command, names []string, localOnly bool) error {
	if err := a.ensureConfig(); err != nil {
		return err
	}
	rows := accounts.BuildListRows(a.Paths, a.Config, a.Client, localOnly, a.Now())
	filtered := accounts.ListRowsResult{
		Rows:           []accounts.ListRow{},
		CurrentIndices: map[int]struct{}{},
		Notes:          []string{},
	}
	nameSet := map[string]struct{}{}
	for _, name := range names {
		nameSet[name] = struct{}{}
	}
	for index, row := range rows.Rows {
		accountName := row.Account
		if strings.Contains(accountName, " <") {
			accountName = strings.SplitN(accountName, " <", 2)[0]
		}
		if _, ok := nameSet[accountName]; !ok {
			continue
		}
		filtered.Rows = append(filtered.Rows, row)
		if _, ok := rows.CurrentIndices[index]; ok {
			filtered.CurrentIndices[len(filtered.Rows)-1] = struct{}{}
		}
	}
	for _, note := range rows.Notes {
		name := strings.SplitN(note, ":", 2)[0]
		if _, ok := nameSet[name]; ok {
			filtered.Notes = append(filtered.Notes, note)
		}
	}
	printListRows(cmd, filtered)
	return nil
}

func completeAccountNames(paths config.Paths) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		all := accounts.ListAccountNames(paths)
		filtered := []string{}
		for _, name := range all {
			if strings.HasPrefix(name, toComplete) {
				filtered = append(filtered, name)
			}
		}
		return filtered, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeRenameArgs(paths config.Paths) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return completeAccountNames(paths)(nil, nil, toComplete)
	}
}

func shouldSkipRuntimePreparation(cmd *cobra.Command) bool {
	skipped := []string{"completion", "install-completion", "__complete", "__completeNoDesc"}
	for current := cmd; current != nil; current = current.Parent() {
		if slices.Contains(skipped, current.Name()) {
			return true
		}
	}
	return false
}

func resolveCodexBin(cfg config.Config) string {
	if fromEnv := strings.TrimSpace(os.Getenv("CODEX_SWITCH_CODEX_BIN")); fromEnv != "" {
		return fromEnv
	}
	if fromConfig := strings.TrimSpace(cfg.CodexBin); fromConfig != "" {
		return fromConfig
	}
	path, _ := execLookPath("codex")
	return path
}

func (a *App) ensureConfig() error {
	if a.Client == nil {
		a.Client = &http.Client{}
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.ConfigLoaded {
		return nil
	}
	cfg, err := config.Load(a.Paths)
	if err != nil {
		return err
	}
	a.Config = cfg
	a.ConfigLoaded = true
	return nil
}

func printWarningsOrSummary(cmd *cobra.Command, warnings, refreshNotes, refreshedNames, updated []string, force bool) error {
	allNotes := append([]string{}, warnings...)
	allNotes = append(allNotes, refreshNotes...)
	if len(allNotes) > 0 {
		for _, note := range allNotes {
			fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle(note, ansiFeatureWarningStyle))
		}
		return nil
	}

	if len(refreshedNames) > 0 {
		prefix := "Refreshed"
		if force {
			prefix = "Force refreshed"
		}
		fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("%s: %s", prefix, strings.Join(refreshedNames, ", "))))
	}
	if len(updated) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), colorize(fmt.Sprintf("Synced aliases: %s", strings.Join(updated, ", "))))
	}
	if len(refreshedNames) == 0 && len(updated) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), colorize("Already up to date"))
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}

func filterStartupWarnings(warnings []string) []string {
	filtered := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		switch warning {
		case "Not logged in.", "No saved aliases match the current account.":
			continue
		default:
			filtered = append(filtered, warning)
		}
	}
	return filtered
}

func confirmAction(cmd *cobra.Command, assumeYes bool, prompt string) error {
	if assumeYes {
		return nil
	}

	confirmed, err := confirmOptionalAction(cmd, prompt)
	if err != nil {
		return err
	}
	if confirmed {
		return nil
	}
	return fmt.Errorf("cancelled")
}

func confirmOptionalAction(cmd *cobra.Command, prompt string) (bool, error) {
	input := cmd.InOrStdin()
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s ", colorizeWithStyle(prompt, ansiFeatureWarningStyle), colorizeWithStyle("[y/N]", ansiFeatureLabelStyle))
	reader := bufio.NewReader(input)
	reply, err := reader.ReadString('\n')
	if err != nil && len(reply) == 0 {
		if file, ok := input.(*os.File); ok {
			if info, statErr := file.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice == 0 {
				return false, fmt.Errorf("%s rerun with --yes or pipe `yes` to confirm", prompt)
			}
		}
		return false, fmt.Errorf("confirmation cancelled")
	}
	reply = strings.ToLower(strings.TrimSpace(reply))
	if reply == "y" || reply == "yes" {
		return true, nil
	}
	return false, nil
}

func filterCurrentRows(rows accounts.ListRowsResult) accounts.ListRowsResult {
	filtered := accounts.ListRowsResult{
		Rows:           []accounts.ListRow{},
		CurrentIndices: map[int]struct{}{},
		Notes:          []string{},
	}
	for index, row := range rows.Rows {
		if _, ok := rows.CurrentIndices[index]; !ok {
			continue
		}
		filtered.Rows = append(filtered.Rows, row)
		filtered.CurrentIndices[len(filtered.Rows)-1] = struct{}{}
	}
	for _, note := range rows.Notes {
		name := strings.SplitN(note, ":", 2)[0]
		for _, row := range filtered.Rows {
			if strings.HasPrefix(row.Account, name+" <") || row.Account == name {
				filtered.Notes = append(filtered.Notes, note)
			}
		}
	}
	return filtered
}

func printListRows(cmd *cobra.Command, result accounts.ListRowsResult) {
	rows := make([][]string, 0, len(result.Rows))
	rowStyles := make([][]string, 0, len(result.Rows))
	for index, row := range result.Rows {
		rows = append(rows, []string{row.Marker, row.Ready, row.Account, row.Plan, row.FiveHour, row.Weekly, row.LastChecked})
		style := ansiListRowStyle
		if _, ok := result.CurrentIndices[index]; ok {
			style = ansiListCurrentRowStyle
		}
		rowStyles = append(rowStyles, []string{style, style, style, style, style, style, style})
	}
	printColorTable(
		cmd.OutOrStdout(),
		[]string{"", "READY", "ACCOUNT", "PLAN", "5H USAGE", "WEEKLY USAGE", "LAST CHECKED"},
		rows,
		rowStyles,
		ansiListHeaderStyle,
	)
	printNotes(cmd, result.Notes)
}

func lifetime(token string) string {
	issued := auth.IssuedAtUnix(token)
	expires := auth.ExpirationUnix(token)
	if issued == nil || expires == nil {
		return "-"
	}
	return support.MustDurationString(time.Unix(*expires, 0).Sub(time.Unix(*issued, 0)))
}

func refreshTokenDetails(token string) string {
	if strings.TrimSpace(token) == "" {
		return "-"
	}
	if auth.IssuedAtUnix(token) != nil || auth.ExpirationUnix(token) != nil {
		return "jwt"
	}
	return "opaque"
}

func present(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "present"
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func status(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
}

func boolStatus(value bool, truthy, falsy string) string {
	if value {
		return truthy
	}
	return falsy
}

func colorize(text string) string {
	return colorizeWithStyle(text, ansiFeatureSuccessStyle)
}

const (
	ansiReset               = "\033[0m"
	ansiFeatureLabelStyle   = "\033[38;5;110m"
	ansiFeatureValueStyle   = "\033[38;5;245m"
	ansiFeatureInfoStyle    = "\033[38;5;245m"
	ansiFeatureSuccessStyle = "\033[38;5;151m"
	ansiFeatureCommandStyle = "\033[38;5;151m"
	ansiFeatureWarningStyle = "\033[38;5;214m"
	ansiHelpTitleStyle      = "\033[1;97m"
	ansiHelpSectionStyle    = "\033[1;4;97m"
	ansiHelpLeftStyle       = "\033[1;97m"
	ansiHelpRightStyle      = "\033[38;5;245m"
	ansiHelpUsageStyle      = "\033[37m"
	ansiListHeaderStyle     = "\033[38;5;110m"
	ansiListRowStyle        = "\033[38;5;245m"
	ansiListCurrentRowStyle = "\033[38;5;151m"
)

func colorizeWithStyle(text, style string) string {
	if !isTTY() || strings.TrimSpace(style) == "" {
		return text
	}
	return style + text + ansiReset
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func printHeadline(writer interface{ Write([]byte) (int, error) }, text string) {
	fmt.Fprintln(writer, colorizeWithStyle(text, ansiFeatureLabelStyle))
}

func printSuccess(writer interface{ Write([]byte) (int, error) }, text string) {
	fmt.Fprintln(writer, colorizeWithStyle(text, ansiFeatureSuccessStyle))
}

func printInfo(writer interface{ Write([]byte) (int, error) }, text string) {
	fmt.Fprintln(writer, colorizeWithStyle(text, ansiFeatureInfoStyle))
}

func printInfoWarning(writer interface{ Write([]byte) (int, error) }, text string) {
	fmt.Fprintln(writer, colorizeWithStyle(text, ansiFeatureWarningStyle))
}

func printCommand(writer interface{ Write([]byte) (int, error) }, text string) {
	fmt.Fprintln(writer, colorizeWithStyle(text, ansiFeatureCommandStyle))
}

func printKeyValue(writer interface{ Write([]byte) (int, error) }, key, value string) {
	line := fmt.Sprintf("%s: %s", colorizeWithStyle(key, ansiFeatureLabelStyle), colorizeWithStyle(value, ansiFeatureValueStyle))
	if !isTTY() {
		line = fmt.Sprintf("%s: %s", key, value)
	}
	fmt.Fprintln(writer, line)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func renderHelp(writer interface{ Write([]byte) (int, error) }, cmd *cobra.Command) {
	title := strings.TrimSpace(cmd.Short)
	if title == "" {
		title = cmd.CommandPath()
	}
	if cmd.Parent() == nil {
		title = "Codex Switch CLI"
	}
	printHelpTitle(writer, title)
	if long := strings.TrimSpace(cmd.Long); long != "" && long != title {
		fmt.Fprintln(writer, colorizeWithStyle(long, ansiHelpRightStyle))
	}
	fmt.Fprintln(writer)

	printHelpSectionHeader(writer, "Usage:")
	printHelpUsageLines(writer, helpUsageLines(cmd))

	commands := availableCommandRows(cmd)
	if len(commands) > 0 {
		fmt.Fprintln(writer)
		printHelpSectionHeader(writer, "Commands:")
		printHelpRows(writer, commands)
	}

	options := optionRows(cmd)
	if len(options) > 0 {
		fmt.Fprintln(writer)
		printHelpSectionHeader(writer, "Options:")
		printHelpRows(writer, options)
	}
}

type helpRow struct {
	Left  string
	Right string
}

func helpUsageLines(cmd *cobra.Command) []string {
	lines := []string{normalizeHelpUsage(cmd.UseLine())}
	parent := cmd.Parent()
	if parent == nil && cmd.HasAvailableSubCommands() {
		lines = append(lines, normalizeHelpUsage(fmt.Sprintf("%s [OPTIONS] <COMMAND> [ARGS]", cmd.CommandPath())))
	}
	return lines
}

func normalizeHelpUsage(line string) string {
	replacer := strings.NewReplacer(
		"[flags]", "[OPTIONS]",
		"[Flags]", "[OPTIONS]",
		"[flags...]", "[OPTIONS]",
		"[Flags...]", "[OPTIONS]",
	)
	return replacer.Replace(line)
}

func availableCommandRows(cmd *cobra.Command) []helpRow {
	rows := []helpRow{}
	for _, child := range cmd.Commands() {
		if !child.IsAvailableCommand() || child.Hidden {
			continue
		}
		right := child.Short
		if len(child.Aliases) > 0 {
			right = fmt.Sprintf("%s [aliases: %s]", right, strings.Join(child.Aliases, ", "))
		}
		rows = append(rows, helpRow{Left: child.Name(), Right: right})
	}
	sortHelpRows(cmd, rows)
	return rows
}

func sortHelpRows(cmd *cobra.Command, rows []helpRow) {
	if len(rows) < 2 {
		return
	}

	order := map[string]int{}
	if cmd.Parent() == nil {
		order = map[string]int{
			"login":              0,
			"list":               1,
			"current":            2,
			"use":                3,
			"save":               4,
			"sync":               5,
			"token-info":         6,
			"rename":             7,
			"delete":             8,
			"prune":              9,
			"doctor":             10,
			"install-completion": 11,
			"completion":         12,
			"help":               13,
		}
	}

	slices.SortStableFunc(rows, func(left, right helpRow) int {
		leftRank, leftOk := order[left.Left]
		rightRank, rightOk := order[right.Left]
		switch {
		case leftOk && rightOk:
			return leftRank - rightRank
		case leftOk:
			return -1
		case rightOk:
			return 1
		default:
			return strings.Compare(left.Left, right.Left)
		}
	})
}

func optionRows(cmd *cobra.Command) []helpRow {
	flags := []*pflag.Flag{}
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, flag)
	})
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, flag)
	})

	rows := make([]helpRow, 0, len(flags))
	for _, flag := range flags {
		rows = append(rows, helpRow{
			Left:  formatFlagLabel(flag),
			Right: formatFlagUsage(flag),
		})
	}
	return rows
}

func formatFlagLabel(flag *pflag.Flag) string {
	parts := []string{}
	if flag.Shorthand != "" {
		parts = append(parts, "-"+flag.Shorthand)
	}
	parts = append(parts, "--"+flag.Name)
	label := strings.Join(parts, ", ")
	if flag.Value.Type() != "bool" {
		label += " <" + strings.ToUpper(flag.Value.Type()) + ">"
	}
	return label
}

func formatFlagUsage(flag *pflag.Flag) string {
	usage := flag.Usage
	if flag.DefValue == "" || flag.DefValue == "false" || flag.DefValue == "[]" {
		return usage
	}
	return fmt.Sprintf("%s (default: %s)", usage, flag.DefValue)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
