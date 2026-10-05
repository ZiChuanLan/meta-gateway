package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out, nil
}

func readFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// escapeCell makes a value safe inside a markdown table cell.
func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return strings.TrimSpace(value)
}

// table renders a markdown table. headers and every row must have equal length.
func table(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = escapeCell(cell)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return b.String()
}

// orDash renders an empty value as an em dash so empty cells read as "nothing
// here" rather than "the generator failed".
func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func heading(level int, text string) string {
	return strings.Repeat("#", level) + " " + text + "\n\n"
}

func paragraph(lines ...string) string {
	return strings.Join(lines, "\n") + "\n\n"
}

func bullet(items []string) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString("- " + item + "\n")
	}
	return b.String() + "\n"
}

func codeBlock(language, body string) string {
	return "```" + language + "\n" + strings.TrimRight(body, "\n") + "\n```\n\n"
}

// difference returns the items of `from` that are not present in `against`,
// preserving order. It is how the generator surfaces the two-catalogs-drifted
// case (a backend category the console cannot classify, say) instead of
// silently picking one side.
func difference(from []string, against map[string]bool) []string {
	var out []string
	for _, item := range from {
		if !against[item] {
			out = append(out, item)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sortStrings(out)
	return out
}

func sortStrings(values []string) {
	// Insertion sort keeps this file dependency-free for a handful of items;
	// the tables are small by construction.
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sortStrings(out)
	return out
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, "、")
}

func countNote(n int, unit string) string {
	return fmt.Sprintf("共 **%d** 个%s。\n\n", n, unit)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
