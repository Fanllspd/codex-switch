package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
)

func printTable(writer io.Writer, headers []string, rows [][]string) {
	printStyledTable(writer, headers, rows, nil)
}

func printStyledTable(writer io.Writer, headers []string, rows [][]string, rowStyles []string) {
	printAlignedTable(writer, headers, rows, rowStyles)
}

func printColorTable(writer io.Writer, headers []string, rows [][]string, cellStyles [][]string, headerStyle string) {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = displayWidth(header)
	}
	for _, row := range rows {
		for index, value := range row {
			if index >= len(widths) {
				continue
			}
			if width := displayWidth(value); width > widths[index] {
				widths[index] = width
			}
		}
	}

	writeRow := func(cells []string, styles []string) {
		parts := make([]string, 0, len(cells))
		for index, cell := range cells {
			padded := cell
			if index < len(widths) {
				padded = padRight(cell, widths[index])
			}
			style := ""
			if index < len(styles) {
				style = styles[index]
			}
			parts = append(parts, colorizeWithStyle(padded, style))
		}
		fmt.Fprintln(writer, strings.TrimRight(strings.Join(parts, "  "), " "))
	}

	headerStyles := make([]string, len(headers))
	for index := range headerStyles {
		headerStyles[index] = headerStyle
	}
	writeRow(headers, headerStyles)

	for index, row := range rows {
		styles := []string{}
		if index < len(cellStyles) {
			styles = cellStyles[index]
		}
		writeRow(row, styles)
	}
}

func printPlainTable(writer io.Writer, headers []string, rows [][]string) {
	printAlignedTable(writer, headers, rows, nil)
}

func printAlignedTable(writer io.Writer, headers []string, rows [][]string, rowStyles []string) {
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = displayWidth(header)
	}
	for _, row := range rows {
		for index, value := range row {
			if index >= len(widths) {
				continue
			}
			if width := displayWidth(value); width > widths[index] {
				widths[index] = width
			}
		}
	}

	writeLine := func(cells []string, style string) {
		var parts []string
		for index, cell := range cells {
			if index >= len(widths) {
				parts = append(parts, cell)
				continue
			}
			parts = append(parts, padRight(cell, widths[index]))
		}
		line := strings.TrimRight(strings.Join(parts, "  "), " ")
		if style != "" {
			line = colorizeWithStyle(line, style)
		}
		fmt.Fprintln(writer, line)
	}

	headerStyle := ""
	if isTTY() {
		headerStyle = ansiFeatureLabelStyle
	}
	writeLine(headers, headerStyle)
	for index, row := range rows {
		style := ""
		if index < len(rowStyles) {
			style = rowStyles[index]
		}
		writeLine(row, style)
	}
}

func printNotes(cmd interface{ OutOrStdout() io.Writer }, notes []string) {
	if len(notes) == 0 {
		return
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), colorizeWithStyle("Notes:", ansiFeatureLabelStyle))
	for _, note := range notes {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", colorizeWithStyle("  - "+note, ansiFeatureWarningStyle))
	}
}

func printSectionHeader(writer interface{ Write([]byte) (int, error) }, title string) {
	fmt.Fprintln(writer, colorizeWithStyle(title, ansiFeatureLabelStyle))
}

func printHelpTitle(writer interface{ Write([]byte) (int, error) }, title string) {
	fmt.Fprintln(writer, colorizeWithStyle(title, ansiHelpTitleStyle))
}

func printHelpSectionHeader(writer interface{ Write([]byte) (int, error) }, title string) {
	fmt.Fprintln(writer, colorizeWithStyle(title, ansiHelpSectionStyle))
}

func printHelpUsageLines(writer interface{ Write([]byte) (int, error) }, lines []string) {
	for _, line := range lines {
		fmt.Fprintf(writer, "  %s\n", colorizeWithStyle(line, ansiHelpUsageStyle))
	}
}

func printHelpRows(writer interface{ Write([]byte) (int, error) }, rows []helpRow) {
	leftWidth := 0
	for _, row := range rows {
		if width := displayWidth(row.Left); width > leftWidth {
			leftWidth = width
		}
	}
	if leftWidth < 12 {
		leftWidth = 12
	}
	if leftWidth > 28 {
		leftWidth = 28
	}

	maxWidth := helpWidth()
	rightWidth := maxWidth - leftWidth - 4
	if rightWidth < 24 {
		rightWidth = 24
	}

	for _, row := range rows {
		wrapped := wrapText(row.Right, rightWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for index, line := range wrapped {
			left := ""
			if index == 0 {
				left = colorizeWithStyle(padRight(row.Left, leftWidth), ansiHelpLeftStyle)
			} else {
				left = strings.Repeat(" ", leftWidth)
			}
			right := colorizeWithStyle(line, ansiHelpRightStyle)
			fmt.Fprintf(writer, "  %s  %s\n", left, right)
		}
	}
}

func wrapText(text string, width int) []string {
	if width <= 0 || displayWidth(text) <= width {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	lines := []string{}
	current := words[0]
	for _, word := range words[1:] {
		candidate := current + " " + word
		if displayWidth(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

func helpWidth() int {
	if value := strings.TrimSpace(os.Getenv("COLUMNS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 40 {
			return parsed
		}
	}
	return 96
}

func padRight(text string, targetWidth int) string {
	padding := targetWidth - displayWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		switch {
		case r == '\t':
			width += 4
		case unicode.Is(unicode.Mn, r):
			continue
		case unicode.IsControl(r):
			continue
		case isWideRune(r):
			width += 2
		default:
			width++
		}
	}
	return width
}

func isWideRune(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r):
		return true
	case unicode.Is(unicode.Hangul, r):
		return true
	case unicode.Is(unicode.Hiragana, r):
		return true
	case unicode.Is(unicode.Katakana, r):
		return true
	case r >= 0x1100 && r <= 0x115F:
		return true
	case r >= 0x2E80 && r <= 0xA4CF:
		return true
	case r >= 0xAC00 && r <= 0xD7A3:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0xFE10 && r <= 0xFE19:
		return true
	case r >= 0xFE30 && r <= 0xFE6F:
		return true
	case r >= 0xFF01 && r <= 0xFF60:
		return true
	case r >= 0xFFE0 && r <= 0xFFE6:
		return true
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	default:
		return false
	}
}
