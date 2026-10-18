// Package rotate replaces every live copy of a key with a new one and scrubs
// the old key out of leftovers (backups, history), without spreading the new
// key into them. Databases are never edited; the plan says what to run.
package rotate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/provider"
	"github.com/Blackman99/keyroll/internal/scan"
)

// Action is what happens to one copy.
type Action string

const (
	Write  Action = "write"   // live copy: old key → new key
	Scrub  Action = "scrub"   // leftover: old key → marker, new key NOT added
	Manual Action = "by hand" // database or unreadable: run the printed command
)

// Step is one planned change.
type Step struct {
	Copy   scan.Copy `json:"copy"`
	Action Action    `json:"action"`
	Reason string    `json:"reason,omitempty"`
	Done   bool      `json:"done"`
	Error  string    `json:"error,omitempty"`
}

// Plan for one key.
type Plan struct {
	Key   *scan.Key `json:"key"`
	Steps []Step    `json:"steps"`
}

// Marker replaces the old key in leftovers. It is not key-shaped, so no tool
// and no scanner mistakes it for a credential.
func Marker(fp string) string { return "[revoked-by-keyroll:" + fp + "]" }

// ErrLogin is returned for OAuth/session tokens.
var ErrLogin = errors.New("this is a login token the tool refreshes itself: log out and log in again instead of rotating it")

// MakePlan decides what to do with each copy.
func MakePlan(k *scan.Key) (Plan, error) {
	if k.Kind == provider.KindLogin {
		return Plan{}, ErrLogin
	}
	pl := Plan{Key: k}
	for _, c := range k.Copies {
		st := Step{Copy: c}
		switch c.Class {
		case locate.Live:
			st.Action = Write
		case locate.Residue:
			st.Action, st.Reason = Scrub, "old key removed, new not added"
		default:
			st.Action, st.Reason = Manual, c.Manual
		}
		pl.Steps = append(pl.Steps, st)
	}
	return pl, nil
}

// Counts returns how many steps of each action the plan has.
func (pl Plan) Counts() (write, scrub, manual int) {
	for _, s := range pl.Steps {
		switch s.Action {
		case Write:
			write++
		case Scrub:
			scrub++
		default:
			manual++
		}
	}
	return
}

// ValidateNew checks the new key before anything is written.
func ValidateNew(k *scan.Key, newKey string) (string, error) {
	newKey = strings.TrimSpace(newKey)
	newKey = strings.Trim(newKey, `"'`)
	if newKey == "" {
		return "", errors.New("no new key given")
	}
	if strings.ContainsAny(newKey, " \t\r\n") {
		return "", errors.New("the new key contains spaces or line breaks: paste only the key")
	}
	if newKey == k.Secret() {
		return "", errors.New("the new key is the same as the old one: create a new key in the dashboard first")
	}
	p := k.Prov()
	if p != nil && p.ID != provider.Unknown.ID {
		kind, ok := p.Accepts(newKey)
		if !ok {
			hint := provider.Hint(newKey)
			if g := guessName(newKey); g != "" && g != p.Name {
				return "", fmt.Errorf("that is %s key (%s), not %s key: nothing written", article(g), hint, article(p.Name))
			}
			return "", fmt.Errorf("that does not look like a %s key (%s): nothing written", p.Name, hint)
		}
		if kind == provider.KindLogin {
			return "", errors.New("that is a login token, not an API key")
		}
	}
	return newKey, nil
}

func guessName(v string) string {
	for _, m := range provider.Find([]byte(v)) {
		if m.Provider.ID != provider.Unknown.ID {
			return m.Provider.Name
		}
	}
	return ""
}

// Options for Apply.
type Options struct {
	BackupRoot string // default ~/.keyroll/backups
	Home       string // backups mirror paths relative to this
	NoBackup   bool
	Now        time.Time
}

// Result of Apply.
type Result struct {
	Plan      Plan   `json:"plan"`
	BackupDir string `json:"backup_dir,omitempty"`
	Changed   int    `json:"changed"`
}

// Apply executes the plan. Every file is rewritten atomically (temp file in
// the same directory, fsync, rename) with its original mode; symlinks are
// written through to their target so dotfile managers keep working.
func Apply(pl Plan, newKey string, o Options) (Result, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	old := []byte(pl.Key.Secret())
	res := Result{Plan: pl}
	if !o.NoBackup {
		res.BackupDir = filepath.Join(o.BackupRoot, o.Now.Format("20060102-150405")+"-"+pl.Key.Fingerprint)
	}
	for i := range res.Plan.Steps {
		st := &res.Plan.Steps[i]
		if st.Action == Manual {
			continue
		}
		target := st.Copy.Path
		if st.Copy.RealPath != "" {
			target = st.Copy.RealPath
		}
		repl := []byte(newKey)
		if st.Action == Scrub {
			repl = []byte(Marker(pl.Key.Fingerprint))
		}
		if err := rewrite(target, old, repl, res.BackupDir, relTo(o.Home, target)); err != nil {
			st.Error = err.Error()
			continue
		}
		st.Done = true
		res.Changed++
	}
	return res, nil
}

func relTo(home, p string) string {
	if home != "" {
		if r, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	r := strings.TrimLeft(filepath.ToSlash(filepath.Clean(p)), "/")
	return strings.ReplaceAll(r, ":", "")
}

func rewrite(path string, old, repl []byte, backupDir, rel string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	n := bytes.Count(data, old)
	if n == 0 {
		return errors.New("no longer contains the key (changed since the scan?)")
	}
	if backupDir != "" {
		if err := backup(backupDir, path, rel, data); err != nil {
			return fmt.Errorf("backup failed, file left untouched: %w", err)
		}
	}
	out := bytes.ReplaceAll(data, old, repl)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".keyroll-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil && !isWindows() {
		cleanup()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// Last check: the file must not have changed while we were working.
	now, err := os.ReadFile(path)
	if err != nil || sha(now) != sha(data) {
		os.Remove(tmpName)
		return errors.New("file changed while rotating; left untouched, run keyroll again")
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func backup(dir, path, rel string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "MANIFEST.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\n", path, rel)
	return err
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func isWindows() bool { return os.PathSeparator == '\\' }

// article puts "a" or "an" in front of a provider name ("an OpenAI", "a Groq").
func article(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}
