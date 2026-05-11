package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

var (
	format = "json"
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

func SetFormat(f string) {
	f = strings.ToLower(f)
	switch f {
	case "json", "table", "yaml":
		format = f
	default:
		format = "json"
	}
}

func GetFormat() string { return format }

// SetWriters overrides stdout/stderr for output capture (used by MCP server).
// Pass nil to reset to os.Stdout/os.Stderr.
func SetWriters(out, err io.Writer) {
	if out != nil {
		stdout = out
	} else {
		stdout = os.Stdout
	}
	if err != nil {
		stderr = err
	} else {
		stderr = os.Stderr
	}
}

// Result outputs data in the configured format.
func Result(data any) {
	switch format {
	case "yaml":
		b, err := yaml.Marshal(data)
		if err != nil {
			Error("format_error", err.Error(), 1)
			return
		}
		fmt.Fprint(stdout, string(b))
	case "table":
		printTable(data)
	default:
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			Error("format_error", err.Error(), 1)
			return
		}
		fmt.Fprintln(stdout, string(b))
	}
}

// Error outputs a structured error.
func Error(code string, message string, status int) {
	e := map[string]any{
		"error":   true,
		"code":    code,
		"message": message,
		"status":  status,
	}
	b, _ := json.MarshalIndent(e, "", "  ")
	fmt.Fprintln(stderr, string(b))
}

// printTable handles any data by marshal/unmarshal to normalize types.
func printTable(data any) {
	b, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintln(stdout, err)
		return
	}

	// Try as array of objects
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err == nil && len(rows) > 0 {
		printMapAnySliceTable(rows)
		return
	}

	// Try as single object
	var m map[string]any
	if err := json.Unmarshal(b, &m); err == nil {
		for k, val := range m {
			fmt.Fprintf(stdout, "%s: %v\n", k, val)
		}
		return
	}

	// Fallback
	fmt.Fprintln(stdout, string(b))
}

func printMapAnySliceTable(rows []map[string]any) {
	if len(rows) == 0 {
		return
	}
	keys := make([]string, 0)
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(keys, "\t"))
	for _, row := range rows {
		vals := make([]string, len(keys))
		for i, k := range keys {
			vals[i] = fmt.Sprintf("%v", row[k])
		}
		fmt.Fprintln(w, strings.Join(vals, "\t"))
	}
	w.Flush()
}
