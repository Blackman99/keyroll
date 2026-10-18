// Package render prints keyroll results for people (text) and programs (JSON).
package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Blackman99/keyroll/internal/check"
	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/rotate"
	"github.com/Blackman99/keyroll/internal/scan"
)

// Style holds ANSI codes (empty when color is off).
type Style struct {
	Bold, Dim, Red, Green, Yellow, Blue, Magenta, Cyan, Reset string
	Width                                                     int
}

// NewStyle enables color only for terminals, honouring NO_COLOR and FORCE_COLOR.
func NewStyle(w io.Writer) Style {
	s := Style{Width: 100}
	color := false
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			color = true
			if c := termWidth(f); c >= 60 {
				s.Width = min(c, 160) // the real terminal width: shells rarely export COLUMNS
			}
		}
	}
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c >= 60 {
		s.Width = c
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		color = false
	}
	if os.Getenv("FORCE_COLOR") != "" && os.Getenv("FORCE_COLOR") != "0" {
		color = true
	}
	if color {
		s.Bold, s.Dim, s.Reset = "\x1b[1m", "\x1b[2m", "\x1b[0m"
		s.Red, s.Green, s.Yellow = "\x1b[38;5;203m", "\x1b[38;5;114m", "\x1b[38;5;221m"
		s.Blue, s.Magenta, s.Cyan = "\x1b[38;5;75m", "\x1b[38;5;176m", "\x1b[38;5;80m"
	}
	return s
}

func (s Style) c(code, text string) string {
	if code == "" {
		return text
	}
	return code + text + s.Reset
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%s %s", comma(n), many)
}

func comma(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// middle-truncate long paths so columns stay aligned
func fit(s string, n int) string {
	if utf8.RuneCountInString(s) <= n || n < 10 {
		return s
	}
	r := []rune(s)
	head := n/2 - 1
	tail := n - head - 1
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

func pad(s string, n int) string {
	if k := utf8.RuneCountInString(s); k < n {
		return s + strings.Repeat(" ", n-k)
	}
	return s
}

// collapseBackups folds keyroll's own backup files into one line per backup folder.
func collapseBackups(cs []scan.Copy) []scan.Copy {
	var out []scan.Copy
	idx := map[string]int{}
	for _, c := range cs {
		if c.Tool != "keyroll backup" {
			out = append(out, c)
			continue
		}
		d := c.Display
		if i := strings.Index(d, "/backups/"); i >= 0 {
			rest := d[i+len("/backups/"):]
			if j := strings.Index(rest, "/"); j >= 0 {
				d = d[:i+len("/backups/")+j+1]
			}
		}
		if n, ok := idx[d]; ok {
			out[n].Count++
			continue
		}
		idx[d] = len(out)
		out = append(out, scan.Copy{Display: d, Tool: c.Tool, Class: c.Class, Count: 1,
			Note: "keyroll's own backup of the originals: delete it once the old key is revoked"})
	}
	for i := range out {
		if out[i].Tool == "keyroll backup" && out[i].Count > 1 {
			out[i].Field = fmt.Sprintf("%d files", out[i].Count)
			out[i].Count = 1
		}
	}
	return out
}

// Manual fills the provider into a copy's manual command.
func Manual(c scan.Copy, providerID string) string {
	return strings.ReplaceAll(c.Manual, "<provider>", providerID)
}

// ClassLabel is the word shown for a copy class.
func ClassLabel(c locate.Class) string {
	switch c {
	case locate.Live:
		return "live"
	case locate.Database:
		return "database"
	}
	return "leftover"
}

func (s Style) classColor(c locate.Class) string {
	switch c {
	case locate.Live:
		return s.Bold
	case locate.Database:
		return s.Blue
	}
	return s.Dim
}

func where(c scan.Copy) string {
	p := c.Display
	if len(c.Lines) > 0 {
		p += ":" + strconv.Itoa(c.Lines[0])
		if len(c.Lines) > 1 {
			p += fmt.Sprintf(" (+%d)", len(c.Lines)-1)
		}
	} else if c.Count > 1 {
		p += fmt.Sprintf(" (×%d)", c.Count)
	}
	return p
}

// Map prints the copy map.
func Map(w io.Writer, s Style, r *scan.Report, spillscanOnPath bool) {
	if len(r.Keys) == 0 {
		fmt.Fprintf(w, "%s  no AI provider keys found in %s and %s\n", s.c(s.Bold, "keyroll"),
			plural(r.FilesScanned, "known file", "known files"), plural(r.DirsWalked, "folder", "folders"))
		fmt.Fprintf(w, "%s\n", s.c(s.Dim, "  nothing was sent anywhere. `keyroll paths` lists every place it looks."))
		notScanned(w, s, spillscanOnPath)
		return
	}
	fmt.Fprintf(w, "%s  %s · %s · %s %s\n\n", s.c(s.Bold, "keyroll"), plural(len(r.Keys), "key", "keys"),
		plural(r.Copies(), "copy", "copies"), plural(r.Tools(), "tool", "tools"), s.c(s.Dim, "· offline, nothing sent"))
	pathW := s.Width - 34
	if pathW < 30 {
		pathW = 30
	}
	// one path column for the whole map, so the field names line up across keys
	colW := 0
	for _, k := range r.Keys {
		for _, c := range collapseBackups(k.Copies) {
			if n := utf8.RuneCountInString(fit(where(c), pathW)); n > colW {
				colW = n
			}
		}
	}
	for _, k := range r.Keys {
		live, db, left := k.Count(locate.Live), k.Count(locate.Database), k.Count(locate.Residue)
		parts := []string{}
		if live > 0 {
			parts = append(parts, fmt.Sprintf("%d live", live))
		}
		if db > 0 {
			parts = append(parts, fmt.Sprintf("%d database", db))
		}
		if left > 0 {
			parts = append(parts, plural(left, "leftover", "leftovers"))
		}
		breakdown := strings.Join(parts, " · ")
		if len(k.Copies) == 1 && len(parts) == 1 {
			breakdown = strings.TrimPrefix(parts[0], "1 ") // "1 copy  live", not "1 copy  1 live"
		}
		dot := s.c(s.Yellow, "●")
		if len(k.Copies) == 1 {
			dot = s.c(s.Dim, "○")
		}
		kind := ""
		if k.Kind == "login" {
			kind = s.c(s.Dim, " login token")
		} else if k.Kind == "admin" {
			kind = s.c(s.Magenta, " admin key")
		}
		fmt.Fprintf(w, "  %s %s %s  %s  %s%s\n", dot, s.c(s.Bold, pad(k.ProviderNam, 17)), pad(k.Hint, 15),
			s.c(s.Cyan, "#"+k.Fingerprint), s.c(s.Bold, plural(len(k.Copies), "copy", "copies")),
			s.c(s.Dim, "  "+breakdown)+kind)
		copies := collapseBackups(k.Copies)
		for _, c := range copies {
			label := s.c(s.classColor(c.Class), pad(ClassLabel(c.Class), 9))
			field := ""
			if c.Field != "" {
				field = "  " + s.c(s.Dim, c.Field)
			}
			fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("      %s %s %s%s", label, pad(fit(c.Tool, 16), 16), pad(fit(where(c), pathW), colW), field), " "))
			if c.RealPath != "" {
				fmt.Fprintf(w, "      %s %s\n", strings.Repeat(" ", 26), s.c(s.Dim, "→ symlink to "+fit(c.RealPath, pathW)))
			}
			if c.GitRepo != "" {
				fmt.Fprintf(w, "      %s %s\n", strings.Repeat(" ", 26), s.c(s.Yellow, "tracked by git in "+fit(c.GitRepo, pathW-20)+": history keeps the old key, so revoke it"))
			}
			if c.Manual != "" {
				fmt.Fprintf(w, "      %s %s\n", strings.Repeat(" ", 26), s.c(s.Blue, "→ "+Manual(c, k.Provider)))
			} else if c.Note != "" && c.Class != locate.Live {
				fmt.Fprintf(w, "      %s %s\n", strings.Repeat(" ", 26), s.c(s.Dim, c.Note))
			}
		}
		fmt.Fprintln(w)
	}
	var top *scan.Key
	for _, k := range r.Keys {
		if k.Kind != "login" && len(k.Copies) > 1 && k.Count(locate.Live) > 0 {
			top = k
			break
		}
	}
	fmt.Fprintf(w, "  %s  %s  %s\n", s.c(s.Bold, "next"), pad("keyroll check", 24), s.c(s.Dim, "ask each issuer: is each key ALIVE or REJECTED? (one free GET per key)"))
	if top != nil {
		wr, sc, man := 0, 0, 0
		if pl, err := rotate.MakePlan(top); err == nil {
			wr, sc, man = pl.Counts()
		}
		msg := fmt.Sprintf("paste the new key once: %s rewritten", plural(wr, "file", "files"))
		if sc > 0 {
			msg += fmt.Sprintf(", %s scrubbed", plural(sc, "leftover", "leftovers"))
		}
		if man > 0 {
			msg += fmt.Sprintf(", %d by hand", man)
		}
		fmt.Fprintf(w, "        %s  %s\n", pad("keyroll rotate "+top.Fingerprint, 24), s.c(s.Dim, msg))
	}
	notScanned(w, s, spillscanOnPath)
	fmt.Fprintf(w, "%s\n", s.c(s.Dim, fmt.Sprintf("  scanned %s and %s in %.2fs", plural(r.FilesScanned, "file", "files"), plural(r.DirsWalked, "folder", "folders"), r.Elapsed.Seconds())))
	if len(r.Unreadable) > 0 {
		fmt.Fprintf(w, "%s\n", s.c(s.Yellow, fmt.Sprintf("  could not read %s (permissions): %s", plural(len(r.Unreadable), "file", "files"), strings.Join(first(r.Unreadable, 3), ", "))))
	}
}

func first(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(append([]string{}, xs[:n]...), fmt.Sprintf("+%d more", len(xs)-n))
}

func notScanned(w io.Writer, s Style, spill bool) {
	how := "github.com/Blackman99/spillscan"
	if spill {
		how = "run `spillscan`"
	}
	fmt.Fprintf(w, "  %s  %s\n", s.c(s.Bold, "not here"), s.c(s.Dim, "keys pasted into agent chats live in session transcripts: "+how))
}

// CheckRow pairs a key with its result.
type CheckRow struct {
	Key    *scan.Key
	Result check.Result
}

// Check prints liveness results and returns how many rejected keys are still
// configured somewhere (those tools will fail).
func Check(w io.Writer, s Style, rows []CheckRow) (stale int) {
	asked, alive, rejected, unknown := 0, 0, 0, 0
	for _, r := range rows {
		switch r.Result.State {
		case check.Alive:
			alive++
		case check.Rejected:
			rejected++
		case check.Unknown:
			unknown++
		}
		if r.Result.State != check.Skipped {
			asked++
		}
	}
	fmt.Fprintf(w, "%s  %s · %s %s\n\n", s.c(s.Bold, "keyroll check"), plural(len(rows), "key", "keys"),
		plural(asked, "request", "requests"), s.c(s.Dim, "· each key went only to its own issuer"))
	for _, r := range rows {
		k, res := r.Key, r.Result
		var mark, word string
		switch res.State {
		case check.Alive:
			mark, word = s.c(s.Green, "●"), s.c(s.Green+s.Bold, pad("ALIVE", 9))
		case check.Rejected:
			mark, word = s.c(s.Red, "✕"), s.c(s.Red+s.Bold, pad("REJECTED", 9))
		case check.Unknown:
			mark, word = s.c(s.Yellow, "?"), s.c(s.Yellow, pad("unknown", 9))
		default:
			mark, word = s.c(s.Dim, "–"), s.c(s.Dim, pad("skipped", 9))
		}
		detail := res.Detail
		if res.State == check.Alive && res.NoCap {
			detail = s.c(s.Yellow, detail)
		} else {
			detail = s.c(s.Dim, detail)
		}
		fmt.Fprintf(w, "  %s %s %s %s  %s  %s  %s\n", mark, word, pad(k.ProviderNam, 17), pad(k.Hint, 15),
			s.c(s.Cyan, "#"+k.Fingerprint), pad(plural(realCopies(k), "copy", "copies"), 9), detail)
	}
	fmt.Fprintln(w)
	legend := func(word, color, text string) {
		body := wrap(pad(word, 9)+text, s.Width-4, 11)
		fmt.Fprintf(w, "  %s%s\n", s.c(color, word), s.c(s.Dim, body[len(word):]))
	}
	if alive > 0 {
		legend("ALIVE", s.Green, "the issuer accepts it: whoever holds a copy can spend on it.")
	}
	if rejected > 0 {
		legend("REJECTED", s.Red, "the issuer refuses it: revoked, deleted or mistyped (issuers answer all three the same way). A tool still using it fails with 401 / \"invalid x-api-key\" / \"key expired\": that is the issuer, not a bug in your code.")
	}
	if unknown > 0 {
		legend("unknown", s.Yellow, "no clear answer (network, rate limit, 5xx), counted as neither.")
	}
	if alive+rejected+unknown > 0 {
		fmt.Fprintln(w)
	}
	for _, r := range rows {
		k := r.Key
		live := k.Count(locate.Live) + k.Count(locate.Database)
		switch {
		case r.Result.State == check.Rejected && live > 0:
			stale++
			tools := toolsOf(k)
			verb, their := "still uses", "its"
			if len(tools) > 1 {
				verb, their = "still use", "their"
			}
			code := "an error"
			if r.Result.HTTP > 0 {
				code = fmt.Sprintf("HTTP %d", r.Result.HTTP)
			}
			msg := fmt.Sprintf("%s %s is refused, but %s %s it: %s next request fails with %s.",
				k.ProviderNam, k.Hint, joinAnd(tools), verb, their, code)
			fmt.Fprintf(w, "  %s %s\n", s.c(s.Red, "!"), wrap(msg, s.Width-4, 4))
			fmt.Fprintf(w, "    %s  %s\n", s.c(s.Bold, "keyroll rotate "+k.Fingerprint), s.c(s.Dim, "(paste the new key once)"))
		case r.Result.State == check.Rejected:
			fmt.Fprintf(w, "  %s %s %s is refused by its issuer and only sits in leftovers now: %s\n", s.c(s.Dim, "·"), k.ProviderNam, k.Hint, s.c(s.Dim, "keyroll rotate "+k.Fingerprint+" scrubs them"))
		case r.Result.State == check.Alive && len(k.Copies) > 1:
			msg := fmt.Sprintf("%s %s still works and sits in %s.", k.ProviderNam, k.Hint, plural(len(k.Copies), "place", "places"))
			if r.Result.NoCap {
				msg += " It has no spend limit."
			}
			msg += " If it was ever pasted somewhere you no longer trust (a hosted console, a chat), rotate it:"
			fmt.Fprintf(w, "  %s %s\n", s.c(s.Yellow, "·"), wrap(msg, s.Width-4, 4))
			fmt.Fprintf(w, "    %s\n", s.c(s.Bold, "keyroll rotate "+k.Fingerprint))
		case r.Result.State == check.Unknown:
			fmt.Fprintf(w, "  %s %s %s: %s %s\n", s.c(s.Yellow, "?"), k.ProviderNam, k.Hint, r.Result.Detail, s.c(s.Dim, "· run `keyroll check "+k.Fingerprint+"` again"))
		}
	}
	return stale
}

// realCopies counts copies outside keyroll's own backup folder.
func realCopies(k *scan.Key) int {
	n := 0
	for _, c := range k.Copies {
		if c.Tool != "keyroll backup" {
			n++
		}
	}
	return n
}

func toolsOf(k *scan.Key) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range k.Copies {
		if c.Class != locate.Residue && !seen[c.Tool] {
			seen[c.Tool] = true
			out = append(out, c.Tool)
		}
	}
	return out
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// Plan prints a rotation plan.
func Plan(w io.Writer, s Style, pl rotate.Plan, newHint, newState string) {
	k := pl.Key
	head := fmt.Sprintf("%s  %s %s %s  →  new %s", s.c(s.Bold, "keyroll rotate"), k.ProviderNam, k.Hint, s.c(s.Cyan, "#"+k.Fingerprint), newHint)
	if newState != "" {
		head += " " + newState
	}
	fmt.Fprintf(w, "%s\n\n", head)
	// keep every plan line inside the terminal: the path column gives way to the note
	maxExtra := 0
	for _, st := range pl.Steps {
		if n := utf8.RuneCountInString(planExtra(st, k.Provider)); n > maxExtra {
			maxExtra = min(n, 44)
		}
	}
	pathW := max(30, s.Width-29-maxExtra)
	colW := 0
	for _, st := range pl.Steps {
		if n := utf8.RuneCountInString(fit(where(st.Copy), pathW)); n > colW {
			colW = n
		}
	}
	for _, st := range pl.Steps {
		var a string
		switch st.Action {
		case rotate.Write:
			a = s.c(s.Green, pad("write", 8))
		case rotate.Scrub:
			a = s.c(s.Yellow, pad("scrub", 8))
		default:
			a = s.c(s.Blue, pad("by hand", 8))
		}
		extra := planExtra(st, k.Provider)
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("  %s %s %s  %s", a, pad(fit(st.Copy.Tool, 15), 15), pad(fit(where(st.Copy), pathW), colW), s.c(s.Dim, extra)), " "))
	}
	fmt.Fprintln(w)
}

func planExtra(st rotate.Step, providerID string) string {
	switch st.Action {
	case rotate.Scrub:
		return st.Reason
	case rotate.Manual:
		m := Manual(st.Copy, providerID)
		if i := strings.Index(m, "  (then:"); i > 0 {
			m = m[:i] // the follow-up is in the "now" list after the rotation
		}
		return m
	}
	return st.Copy.Field
}

// Applied prints what rotate did and the steps that remain.
func Applied(w io.Writer, s Style, res rotate.Result, home string, oldState check.State) {
	backups := res.BackupDir
	if home != "" && strings.HasPrefix(backups, home+string(filepath.Separator)) {
		backups = "~" + backups[len(home):]
	}
	k := res.Plan.Key
	failed := 0
	for _, st := range res.Plan.Steps {
		if st.Error != "" {
			failed++
			fmt.Fprintf(w, "  %s %s: %s\n", s.c(s.Red, "✕"), st.Copy.Display, st.Error)
		}
	}
	if failed > 0 {
		fmt.Fprintf(w, "  %s %s failed and those files were left exactly as they were\n", s.c(s.Red, "!"), plural(failed, "step", "steps"))
	}
	fmt.Fprintf(w, "  %s %s changed", s.c(s.Green, "✓"), plural(res.Changed, "file", "files"))
	if res.BackupDir != "" && res.Changed > 0 {
		fmt.Fprintf(w, " · originals (with the OLD key) in %s", backups)
	}
	fmt.Fprintln(w)
	p := k.Prov()
	n := 1
	fmt.Fprintf(w, "\n  %s\n", s.c(s.Bold, "now"))
	for _, st := range res.Plan.Steps {
		if st.Action == rotate.Manual && st.Copy.Manual != "" {
			fmt.Fprintf(w, "  %s\n", wrap(fmt.Sprintf("%d. %s: %s", n, st.Copy.Tool, Manual(st.Copy, k.Provider)), s.Width-2, 5))
			n++
		}
	}
	if p != nil && p.KeysURL != "" {
		if oldState != check.Rejected {
			fmt.Fprintf(w, "  %s\n", wrap(fmt.Sprintf("%d. revoke the old key (the one ending %s): %s", n, k.Hint[len(k.Hint)-4:], p.KeysURL), s.Width-2, 5))
			n++
		}
		cap := p.CapURL
		if cap == "" {
			cap = p.KeysURL
		}
		fmt.Fprintf(w, "  %s\n", wrap(fmt.Sprintf("%d. give the new key a spend limit: %s", n, cap), s.Width-2, 5))
		n++
	}
	fmt.Fprintf(w, "  %s\n", wrap(fmt.Sprintf("%d. restart tools that keep the key in memory (a running Claude Code, Codex or OpenCode session, an open shell: `exec $SHELL`)", n), s.Width-2, 5))
	if res.BackupDir != "" && res.Changed > 0 {
		fmt.Fprintf(w, "  %s\n", s.c(s.Dim, "   once it is revoked, delete the backups: rm -rf "+backups))
	}
}

// OldKey prints the last line of every rotation: whether the issuer still
// accepts the old key, i.e. whether it has actually been revoked.
func OldKey(w io.Writer, s Style, k *scan.Key, res check.Result, haveBackup bool) {
	name := k.ProviderNam
	again := "keyroll check " + k.Fingerprint
	if !haveBackup {
		again = "the dashboard"
		if p := k.Prov(); p != nil && p.KeysURL != "" {
			again = p.KeysURL
		}
	}
	var mark, msg string
	switch res.State {
	case check.Rejected:
		mark = s.c(s.Green, "✓")
		msg = fmt.Sprintf("old key %s: %s now REFUSES it (%s). It is revoked, so the copies you did not find are harmless.", k.Hint, name, res.Detail)
	case check.Alive:
		mark = s.c(s.Red, "✕")
		msg = fmt.Sprintf("old key %s: %s still ACCEPTS it. It is NOT revoked yet: any copy keyroll could not see (a hosted console, CI secrets, another machine) can still spend on it. Revoke it, then confirm with %s.", k.Hint, name, again)
	case check.Unknown:
		mark = s.c(s.Yellow, "?")
		msg = fmt.Sprintf("old key %s: could not ask %s (%s). Treat it as NOT revoked until %s says REJECTED.", k.Hint, name, res.Detail, again)
	default:
		mark = s.c(s.Yellow, "–")
		msg = fmt.Sprintf("old key %s: not checked (%s). Treat it as NOT revoked until you revoke it and %s says REJECTED.", k.Hint, res.Detail, again)
	}
	fmt.Fprintf(w, "\n  %s %s\n", mark, wrap(msg, s.Width-4, 4))
}

// wrap breaks plain text at spaces so no line is wider than width; lines
// after the first are indented by indent spaces.
func wrap(text string, width, indent int) string {
	if width < 40 {
		width = 40
	}
	var b strings.Builder
	col := 0
	for i, word := range strings.Fields(text) {
		n := utf8.RuneCountInString(word)
		if i > 0 {
			if col+1+n > width {
				b.WriteString("\n" + strings.Repeat(" ", indent))
				col = indent
			} else {
				b.WriteByte(' ')
				col++
			}
		}
		b.WriteString(word)
		col += n
	}
	return b.String()
}
