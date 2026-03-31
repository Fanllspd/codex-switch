package support

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func ParseFlexibleDuration(value string) (time.Duration, error) {
	text := strings.TrimSpace(strings.ToLower(value))
	if text == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if digitsOnly(text) {
		seconds, err := strconv.Atoi(text)
		if err != nil {
			return 0, err
		}
		return time.Duration(seconds) * time.Second, nil
	}

	units := map[byte]time.Duration{
		's': time.Second,
		'm': time.Minute,
		'h': time.Hour,
		'd': 24 * time.Hour,
		'w': 7 * 24 * time.Hour,
	}

	suffix := text[len(text)-1]
	unit, ok := units[suffix]
	if !ok {
		return 0, fmt.Errorf("unsupported duration suffix: %q", suffix)
	}

	number := strings.TrimSpace(text[:len(text)-1])
	if number == "" {
		return 0, fmt.Errorf("missing duration value")
	}

	amount, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, err
	}
	if amount < 0 {
		return 0, fmt.Errorf("duration must be non-negative")
	}

	return time.Duration(amount * float64(unit)), nil
}

func MustDurationString(duration time.Duration) string {
	if duration < 0 {
		return "-"
	}

	totalSeconds := int(duration.Round(time.Second) / time.Second)
	days := totalSeconds / int((24*time.Hour)/time.Second)
	totalSeconds %= int((24 * time.Hour) / time.Second)
	hours := totalSeconds / int(time.Hour/time.Second)
	totalSeconds %= int(time.Hour / time.Second)
	minutes := totalSeconds / int(time.Minute/time.Second)
	seconds := totalSeconds % int(time.Minute/time.Second)

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	return strings.Join(parts, " ")
}

func FormatISO8601(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}

	parsed, err := time.Parse(time.RFC3339Nano, strings.ReplaceAll(value, "Z", "+00:00"))
	if err != nil {
		return value
	}

	return parsed.In(time.Local).Format("2006-01-02 15:04")
}

func FormatUnix(unix *int64, now time.Time) string {
	if unix == nil {
		return "-"
	}

	dt := time.Unix(*unix, 0).In(time.Local)
	now = now.In(time.Local)
	if sameDay(dt, now) {
		return dt.Format("15:04")
	}
	return dt.Format("02 Jan 15:04")
}

func ParseISOToTime(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, strings.ReplaceAll(value, "Z", "+00:00"))
	if err != nil {
		return nil
	}

	return &parsed
}

func FormatRelativeAge(when *time.Time, now time.Time) string {
	if when == nil {
		return "-"
	}

	delta := int(math.Max(now.Sub(*when).Seconds(), 0))
	if delta < 3600 {
		minutes := max(delta/60, 1)
		return fmt.Sprintf("%dm ago", minutes)
	}
	if delta < 86400 {
		return fmt.Sprintf("%dh ago", delta/3600)
	}
	return fmt.Sprintf("%dd ago", delta/86400)
}

func digitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
