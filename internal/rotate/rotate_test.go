package rotate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/scan"
	"github.com/Blackman99/keyroll/internal/testhome"
)

func setup(t *testing.T) (string, testhome.Keys, *scan.Key) {
	home := t.TempDir()
	k := testhome.Build(home)
	r, err := scan.Run(scan.Options{Env: locate.Env{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range r.Keys {
		if key.Provider == "anthropic" {
			return home, k, key
		}
	}
	t.Fatal("no anthropic key")
	return "", k, nil
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyWritesLiveScrubsLeftovers(t *testing.T) {
	home, k, key := setup(t)
	pl, err := MakePlan(key)
	if err != nil {
		t.Fatal(err)
	}
	w, s, m := pl.Counts()
	if w != 3 || s != 5 || m != 1 {
		t.Fatalf("plan counts %d/%d/%d", w, s, m)
	}
	res, err := Apply(pl, k.NewAnthropic, Options{BackupRoot: filepath.Join(home, ".keyroll", "backups"), Home: home, Now: time.Unix(1760750000, 0)})
	if err != nil || res.Changed != 8 {
		t.Fatalf("changed=%d err=%v", res.Changed, err)
	}
	cj := read(t, filepath.Join(home, ".claude.json"))
	if !strings.Contains(cj, k.NewAnthropic) || strings.Contains(cj, k.Anthropic) {
		t.Fatal(".claude.json not rewritten")
	}
	var v map[string]any
	if json.Unmarshal([]byte(cj), &v) != nil || v["numStartups"].(float64) != 212 {
		t.Fatal(".claude.json no longer parses the same")
	}
	hist := read(t, filepath.Join(home, ".zsh_history"))
	if strings.Contains(hist, k.NewAnthropic) || strings.Contains(hist, k.Anthropic) || !strings.Contains(hist, Marker(key.Fingerprint)) {
		t.Fatal("history must be scrubbed, and must NOT receive the new key")
	}
	ex := read(t, filepath.Join(home, "code", "shop", ".env.example"))
	if strings.Contains(ex, k.NewAnthropic) {
		t.Fatal("template .env.example must not receive the new key")
	}
	if !strings.Contains(read(t, filepath.Join(home, "code", "shop", ".env")), "OPENAI_API_KEY="+k.OpenAI) {
		t.Fatal("other keys in the same file must be untouched")
	}
	if strings.Contains(read(t, filepath.Join(home, ".local", "share", "opencode", "opencode.db")), k.NewAnthropic) {
		t.Fatal("databases are never edited")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(home, ".claude.json"))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode changed to %v", st.Mode().Perm())
		}
	}
	bk := filepath.Join(res.BackupDir, ".claude.json")
	if !strings.Contains(read(t, bk), k.Anthropic) {
		t.Fatal("backup should hold the original")
	}
	if st, _ := os.Stat(res.BackupDir); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir mode %v", st.Mode().Perm())
	}
	tmps, _ := filepath.Glob(filepath.Join(home, ".keyroll-*.tmp"))
	if len(tmps) != 0 {
		t.Fatal("temp files left behind")
	}
}

func TestSymlinkWrittenThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	home := t.TempDir()
	k := testhome.Build(home)
	dot := filepath.Join(home, "dotfiles")
	os.MkdirAll(dot, 0o755)
	os.Rename(filepath.Join(home, ".zshrc"), filepath.Join(dot, "zshrc"))
	os.Symlink(filepath.Join(dot, "zshrc"), filepath.Join(home, ".zshrc"))
	r, _ := scan.Run(scan.Options{Env: locate.Env{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }}})
	var key *scan.Key
	for _, x := range r.Keys {
		if x.Provider == "anthropic" {
			key = x
		}
	}
	pl, _ := MakePlan(key)
	if _, err := Apply(pl, k.NewAnthropic, Options{NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(filepath.Join(home, ".zshrc"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("~/.zshrc symlink was replaced by a regular file (breaks dotfile managers)")
	}
	if !strings.Contains(read(t, filepath.Join(dot, "zshrc")), k.NewAnthropic) {
		t.Fatal("symlink target not rewritten")
	}
}

func TestValidateNew(t *testing.T) {
	_, k, key := setup(t)
	if _, err := ValidateNew(key, k.Anthropic); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("same key: %v", err)
	}
	if _, err := ValidateNew(key, k.OpenAI); err == nil || !strings.Contains(err.Error(), "OpenAI") {
		t.Fatalf("wrong provider: %v", err)
	}
	if _, err := ValidateNew(key, "  "); err == nil {
		t.Fatal("empty accepted")
	}
	if got, err := ValidateNew(key, "\""+k.NewAnthropic+"\"\n"); err != nil || got != k.NewAnthropic {
		t.Fatalf("quoted paste with newline: %v", err)
	}
}

func TestChangedFileIsLeftAlone(t *testing.T) {
	home, k, key := setup(t)
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# edited meanwhile\n"), 0o644)
	pl, _ := MakePlan(key)
	res, _ := Apply(pl, k.NewAnthropic, Options{NoBackup: true})
	for _, s := range res.Plan.Steps {
		if filepath.Base(s.Copy.Path) == ".zshrc" && (s.Done || s.Error == "") {
			t.Fatalf("expected an error for the changed file: %+v", s)
		}
	}
}
