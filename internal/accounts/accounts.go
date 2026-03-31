package accounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codex-switch/internal/auth"
	"codex-switch/internal/config"
	"codex-switch/internal/support"
	"codex-switch/internal/usage"
)

type Snapshot struct {
	Path            string
	Name            string
	Tokens          map[string]string
	AccountID       string
	Email           string
	Plan            string
	TokenExpiry     string
	LastRefreshTime *time.Time
}

type ListRow struct {
	Marker      string
	Ready       string
	Account     string
	Plan        string
	FiveHour    string
	Weekly      string
	LastChecked string
}

type ListRowsResult struct {
	Rows           []ListRow
	CurrentIndices map[int]struct{}
	Notes          []string
}

type PrunePair struct {
	Keep   string
	Remove string
}

type SyncMeta struct {
	LastChecked  map[string]string `json:"last_checked"`
	CurrentAlias string            `json:"current_alias,omitempty"`
}

const secureFileMode = 0o600

func EnsureAccountsDir(paths config.Paths) error {
	return os.MkdirAll(paths.AccountsDir, 0o755)
}

func ListAccountNames(paths config.Paths) []string {
	files, err := filepath.Glob(filepath.Join(paths.AccountsDir, "*.json"))
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(files))
	for _, path := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	sort.Strings(names)
	return names
}

func DetectCurrentAccountName(paths config.Paths) string {
	files := listAccountFiles(paths)
	if _, err := os.Stat(paths.AuthFile); err != nil {
		return ""
	}

	meta := loadSyncMeta(paths)
	if meta.CurrentAlias != "" {
		currentPath := filepath.Join(paths.AccountsDir, meta.CurrentAlias+".json")
		if _, err := os.Stat(currentPath); err == nil {
			same, sameErr := sameFile(currentPath, paths.AuthFile)
			if sameErr == nil && same {
				return meta.CurrentAlias
			}

			currentID := AccountIDFromFile(paths.AuthFile)
			if currentID != "" && AccountIDFromFile(currentPath) == currentID {
				return meta.CurrentAlias
			}
		}
	}

	for _, path := range files {
		same, err := sameFile(path, paths.AuthFile)
		if err == nil && same {
			return stem(path)
		}
	}

	currentID := AccountIDFromFile(paths.AuthFile)
	if currentID == "" {
		return ""
	}

	for _, path := range files {
		if AccountIDFromFile(path) == currentID {
			return stem(path)
		}
	}

	return ""
}

func ReadSnapshot(path string, now time.Time) (*Snapshot, error) {
	doc, err := auth.LoadDocument(path)
	if err != nil {
		return nil, err
	}

	tokens := auth.Tokens(doc)
	email, plan := auth.ExtractEmailAndPlan(tokens)
	expiration := auth.ExpirationUnix(tokens["access_token"])
	if expiration == nil {
		expiration = auth.ExpirationUnix(tokens["id_token"])
	}

	return &Snapshot{
		Path:            path,
		Name:            stem(path),
		Tokens:          tokens,
		AccountID:       auth.AccountID(tokens),
		Email:           email,
		Plan:            plan,
		TokenExpiry:     support.FormatUnix(expiration, now),
		LastRefreshTime: support.ParseISOToTime(stringValue(doc["last_refresh"])),
	}, nil
}

func BuildListRows(paths config.Paths, cfg config.Config, client *http.Client, localOnly bool, now time.Time) ListRowsResult {
	files := listAccountFiles(paths)
	currentName := DetectCurrentAccountName(paths)
	meta := loadSyncMeta(paths)

	result := ListRowsResult{
		Rows:           []ListRow{},
		CurrentIndices: map[int]struct{}{},
		Notes:          []string{},
	}

	snapshots := map[string]*Snapshot{}
	for _, path := range files {
		snapshot, err := ReadSnapshot(path, now)
		if err != nil {
			lastChecked := support.FormatRelativeAge(support.ParseISOToTime(meta.LastChecked[stem(path)]), now)
			result.Rows = append(result.Rows, ListRow{
				Marker:      " ",
				Ready:       "UNK",
				Account:     stem(path),
				Plan:        "-",
				FiveHour:    "unavailable (-)",
				Weekly:      "unavailable (-)",
				LastChecked: lastChecked,
			})
			result.Notes = append(result.Notes, fmt.Sprintf("%s: read failed: %v", stem(path), err))
			continue
		}
		snapshots[snapshot.Name] = snapshot
	}

	usageByToken := map[string]usageResult{}
	if !localOnly {
		usageByToken = fetchUsageConcurrently(cfg, client, snapshots)
	}

	for _, path := range files {
		snapshot := snapshots[stem(path)]
		if snapshot == nil {
			continue
		}

		row := ListRow{
			Marker:   " ",
			Account:  fmt.Sprintf("%s <%s>", snapshot.Name, snapshot.Email),
			Plan:     snapshot.Plan,
			FiveHour: "local only (-)",
			Weekly:   "local only (-)",
			Ready:    "UNK",
		}

		if snapshot.Name == currentName {
			row.Marker = "*"
		}

		if localOnly {
			row.FiveHour = "local only (-)"
			row.Weekly = "local only (-)"
		} else {
			token := snapshot.Tokens["access_token"]
			fetched := usageByToken[token]
			if fetched.data != nil {
				if email, ok := fetched.data["email"].(string); ok && email != "" {
					row.Account = fmt.Sprintf("%s <%s>", snapshot.Name, email)
				}
				if plan, ok := fetched.data["plan_type"].(string); ok && plan != "" {
					row.Plan = plan
				}
				rateLimit, _ := fetched.data["rate_limit"].(map[string]any)
				if allowed, ok := rateLimit["allowed"].(bool); ok && allowed {
					row.Ready = "YES"
				} else {
					row.Ready = "NO"
				}
				fiveHour, resetFiveHour, weekly, resetWeekly := usage.SummarizeRateLimit(rateLimit, now)
				row.FiveHour = fmt.Sprintf("%s (%s)", fiveHour, resetFiveHour)
				row.Weekly = fmt.Sprintf("%s (%s)", weekly, resetWeekly)
			} else {
				row.Ready = "UNK"
				row.FiveHour = fmt.Sprintf("unavailable (%s)", snapshot.TokenExpiry)
				row.Weekly = "unavailable (-)"
				result.Notes = append(result.Notes, fmt.Sprintf("%s: usage lookup failed: %v", snapshot.Name, fetched.err))
			}
		}

		lastCheckedTime := snapshot.LastRefreshTime
		if lastCheckedTime == nil {
			lastCheckedTime = support.ParseISOToTime(meta.LastChecked[snapshot.Name])
		}
		row.LastChecked = support.FormatRelativeAge(lastCheckedTime, now)
		result.Rows = append(result.Rows, row)
		if snapshot.Name == currentName {
			result.CurrentIndices[len(result.Rows)-1] = struct{}{}
		}
	}

	return result
}

func SyncSavedAliases(paths config.Paths) ([]string, []string, []string) {
	if _, err := os.Stat(paths.AuthFile); err != nil {
		return nil, nil, []string{"Not logged in."}
	}

	currentID := AccountIDFromFile(paths.AuthFile)
	if currentID == "" {
		return nil, nil, []string{"Current auth.json has no account_id."}
	}

	matching := []string{}
	for _, path := range listAccountFiles(paths) {
		if AccountIDFromFile(path) == currentID {
			matching = append(matching, path)
		}
	}
	if len(matching) == 0 {
		return nil, nil, []string{"No saved aliases match the current account."}
	}

	updated := []string{}
	warnings := []string{}
	checkedNames := make([]string, 0, len(matching))
	for _, path := range matching {
		same, err := sameFile(paths.AuthFile, path)
		if err == nil && same {
			checkedNames = append(checkedNames, stem(path))
			continue
		}
		if err := copyFile(paths.AuthFile, path); err == nil {
			updated = append(updated, stem(path))
			checkedNames = append(checkedNames, stem(path))
		} else {
			warnings = append(warnings, fmt.Sprintf("%s: sync failed: %v", stem(path), err))
		}
	}

	return updated, checkedNames, warnings
}

func Save(paths config.Paths, name string, force bool) error {
	if err := validateAccountName(name); err != nil {
		return err
	}
	if _, err := os.Stat(paths.AuthFile); err != nil {
		return fmt.Errorf("Not logged in to Codex.")
	}
	if err := EnsureAccountsDir(paths); err != nil {
		return err
	}

	target := filepath.Join(paths.AccountsDir, name+".json")
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("Account already exists: %s\nUse --force to overwrite it.", name)
	}

	if err := copyFile(paths.AuthFile, target); err != nil {
		return err
	}
	return SetCurrentAlias(paths, name)
}

func Use(paths config.Paths, name string) error {
	if err := validateAccountName(name); err != nil {
		return err
	}

	source := filepath.Join(paths.AccountsDir, name+".json")
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("Account does not exist.")
	}

	if err := copyFile(source, paths.AuthFile); err != nil {
		return err
	}
	return SetCurrentAlias(paths, name)
}

func Rename(paths config.Paths, oldName, newName string) error {
	if err := validateAccountName(oldName); err != nil {
		return fmt.Errorf("invalid old account name: %w", err)
	}
	if err := validateAccountName(newName); err != nil {
		return fmt.Errorf("invalid new account name: %w", err)
	}

	oldPath := filepath.Join(paths.AccountsDir, oldName+".json")
	newPath := filepath.Join(paths.AccountsDir, newName+".json")
	oldInfo, err := os.Stat(oldPath)
	if err != nil {
		return fmt.Errorf("Account does not exist: %s", oldName)
	}
	if newInfo, err := os.Stat(newPath); err == nil {
		if !os.SameFile(oldInfo, newInfo) {
			return fmt.Errorf("Account already exists: %s", newName)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	meta := loadSyncMeta(paths)
	if value, ok := meta.LastChecked[oldName]; ok {
		meta.LastChecked[newName] = value
		delete(meta.LastChecked, oldName)
	}
	if meta.CurrentAlias == oldName {
		meta.CurrentAlias = newName
	}
	_ = saveSyncMeta(paths, meta)

	return nil
}

func Delete(paths config.Paths, name string) error {
	if err := validateAccountName(name); err != nil {
		return err
	}

	target := filepath.Join(paths.AccountsDir, name+".json")
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Account does not exist: %s", name)
		}
		return err
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	meta := loadSyncMeta(paths)
	delete(meta.LastChecked, name)
	if meta.CurrentAlias == name {
		meta.CurrentAlias = ""
	}
	_ = saveSyncMeta(paths, meta)
	return nil
}

func Prune(paths config.Paths, apply bool) ([]PrunePair, error) {
	files := listAccountFiles(paths)
	if len(files) == 0 {
		return nil, nil
	}

	currentName := DetectCurrentAccountName(paths)
	groups := map[string][]string{}
	for _, path := range files {
		snapshot, err := ReadSnapshot(path, time.Now())
		key := ""
		if err == nil && snapshot.AccountID != "" {
			key = "id:" + snapshot.AccountID
		} else {
			bytes, readErr := os.ReadFile(path)
			if readErr != nil {
				key = "path:" + path
			} else {
				key = "sha:" + string(bytes)
			}
		}
		groups[key] = append(groups[key], path)
	}

	pairs := []PrunePair{}
	removeErrors := []error{}
	successfullyRemoved := []string{}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}

		sort.Slice(group, func(i, j int) bool {
			left := stem(group[i])
			right := stem(group[j])
			if left == currentName {
				return true
			}
			if right == currentName {
				return false
			}
			leftInfo, _ := os.Stat(group[i])
			rightInfo, _ := os.Stat(group[j])
			if leftInfo != nil && rightInfo != nil && !leftInfo.ModTime().Equal(rightInfo.ModTime()) {
				return leftInfo.ModTime().After(rightInfo.ModTime())
			}
			return left < right
		})

		keep := stem(group[0])
		for _, path := range group[1:] {
			removeName := stem(path)
			pairs = append(pairs, PrunePair{Keep: keep, Remove: removeName})
			if apply {
				if err := os.Remove(path); err != nil {
					removeErrors = append(removeErrors, fmt.Errorf("%s: %w", removeName, err))
				} else {
					successfullyRemoved = append(successfullyRemoved, removeName)
				}
			}
		}
	}

	if apply {
		meta := loadSyncMeta(paths)
		for _, name := range successfullyRemoved {
			delete(meta.LastChecked, name)
			if meta.CurrentAlias == name {
				meta.CurrentAlias = ""
			}
		}
		_ = saveSyncMeta(paths, meta)
	}

	if len(removeErrors) > 0 {
		return pairs, errors.Join(removeErrors...)
	}

	return pairs, nil
}

func RecordLastChecked(paths config.Paths, names []string, now time.Time) error {
	if len(names) == 0 {
		return nil
	}

	meta := loadSyncMeta(paths)
	for _, name := range names {
		meta.LastChecked[name] = now.UTC().Format(time.RFC3339Nano)
	}
	return saveSyncMeta(paths, meta)
}

func SetCurrentAlias(paths config.Paths, name string) error {
	if err := validateAccountName(name); err != nil {
		return err
	}

	meta := loadSyncMeta(paths)
	meta.CurrentAlias = name
	return saveSyncMeta(paths, meta)
}

func AccountIDFromFile(path string) string {
	doc, err := auth.LoadDocument(path)
	if err != nil {
		return ""
	}
	return auth.AccountID(auth.Tokens(doc))
}

func fetchUsageConcurrently(cfg config.Config, client *http.Client, snapshots map[string]*Snapshot) map[string]usageResult {
	unique := map[string]struct{}{}
	for _, snapshot := range snapshots {
		if token := snapshot.Tokens["access_token"]; token != "" {
			unique[token] = struct{}{}
		}
	}

	results := map[string]usageResult{}
	if len(unique) == 0 {
		return results
	}

	workers := cfg.Network.MaxUsageWorkers
	if workers <= 0 {
		workers = 1
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range jobs {
				data, err := usage.Fetch(client, cfg, token)
				mu.Lock()
				results[token] = usageResult{data: data, err: err}
				mu.Unlock()
			}
		}()
	}

	for token := range unique {
		jobs <- token
	}
	close(jobs)
	wg.Wait()

	return results
}

func listAccountFiles(paths config.Paths) []string {
	files, err := filepath.Glob(filepath.Join(paths.AccountsDir, "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(files)
	return files
}

func sameFile(left, right string) (bool, error) {
	leftBytes, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	rightBytes, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}

func copyFile(source, target string) error {
	bytes, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, bytes, secureFileMode)
}

func loadSyncMeta(paths config.Paths) SyncMeta {
	meta := SyncMeta{LastChecked: map[string]string{}}
	bytes, err := os.ReadFile(paths.SyncMetaFile)
	if err != nil {
		return meta
	}
	_ = jsonUnmarshal(bytes, &meta)
	if meta.LastChecked == nil {
		meta.LastChecked = map[string]string{}
	}
	return meta
}

func saveSyncMeta(paths config.Paths, meta SyncMeta) error {
	bytes, err := jsonMarshalIndent(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(paths.SyncMetaFile, append(bytes, '\n'), secureFileMode)
}

func jsonMarshalIndent(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

func jsonUnmarshal(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

func stem(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func validateAccountName(name string) error {
	text := strings.TrimSpace(name)
	if text == "" {
		return fmt.Errorf("Please provide an account name.")
	}
	if text == "." || text == ".." {
		return fmt.Errorf("account name may only contain letters, numbers, dot, underscore, and hyphen")
	}
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("account name may only contain letters, numbers, dot, underscore, and hyphen")
		}
	}
	return nil
}

type usageResult struct {
	data map[string]any
	err  error
}
