package vm

import (
	"strings"
	"time"
	"unicode"

	units "github.com/docker/go-units"
	"github.com/spf13/cobra"
)

// Clean makes a container-provided string safe to show as one line of
// text: control characters (C0, DEL, C1) and Unicode format characters
// (bidi overrides, zero-width joiners) become U+FFFD or are dropped, and
// the result is bounded to max runes. Names, labels, commands and images
// all come from the container's side; nothing of theirs is interpreted.
func Clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == max {
			b.WriteString("…")
			break
		}
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			r = ' '
		case r < 0x20 || r >= 0x7f && r <= 0x9f:
			r = '�'
		case unicode.Is(unicode.Cf, r):
			continue
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// maxField bounds one table cell.
const maxField = 200

// ShortID is the 12-character form of an ID (a "sha256:" prefix dropped).
func ShortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return Clean(id, 12)
}

// Ago renders the time since t the way `wctr ps` does.
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return units.HumanDuration(now.Sub(t)) + " ago"
}

// Size renders bytes the way `wctr images` does.
func Size(n int64) string { return units.HumanSizeWithPrecision(float64(n), 3) }

// Quote quotes one word for a POSIX shell when it needs it, so a command
// line shown as a teaching aid can be pasted as is.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CLI knows which commands the wctr in use has, so the dashboard shows the
// command line a button stands for, or, where wctr has no such command
// yet, the API call it makes.
type CLI struct{ root *cobra.Command }

// NewCLI reads the command tree of root (wctr's cli.RootCmd).
func NewCLI(root *cobra.Command) *CLI { return &CLI{root: root} }

// Has reports whether wctr has the command name.
func (c *CLI) Has(name string) bool {
	if c == nil || c.root == nil {
		return false
	}
	cmd, _, err := c.root.Find([]string{name})
	return err == nil && cmd != c.root
}

// Line renders "wctr <args...>" with each argument quoted.
func Line(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return "wctr " + strings.Join(q, " ")
}

// Equivalent is the command line for the wctr command args[0], or, when
// wctr has no such command, the API call (method and path) with a note.
func (c *CLI) Equivalent(api string, args ...string) string {
	if c.Has(args[0]) {
		return Line(args...)
	}
	return api + "  (no `wctr " + args[0] + "` yet)"
}
