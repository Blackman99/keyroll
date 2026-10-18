package scan

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/testhome"
)

func env(home string) locate.Env {
	return locate.Env{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }}
}

func byProvider(r *Report, id string) *Key {
	for _, k := range r.Keys {
		if k.Provider == id {
			return k
		}
	}
	return nil
}

func TestDemoHome(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	// a key pasted into a chat: transcripts are spillscan's job, not ours
	tr := filepath.Join(home, ".claude", "projects", "-code-shop", "s.jsonl")
	os.MkdirAll(filepath.Dir(tr), 0o755)
	os.WriteFile(tr, []byte(`{"text":"`+testhome.Fake().Google+`"}`), 0o600)

	r, err := Run(Options{Env: env(home)})
	if err != nil {
		t.Fatal(err)
	}
	an := byProvider(r, "anthropic")
	if an == nil || len(an.Copies) != 9 {
		t.Fatalf("anthropic copies = %v", an)
	}
	if an.Count(locate.Live) != 3 || an.Count(locate.Database) != 1 || an.Count(locate.Residue) != 5 {
		t.Fatalf("classes live=%d db=%d left=%d", an.Count(locate.Live), an.Count(locate.Database), an.Count(locate.Residue))
	}
	for _, c := range an.Copies {
		if filepath.Base(c.Path) == "auth.json" && c.Class != locate.Residue {
			t.Errorf("OpenCode auth.json next to opencode.db must be a leftover, got %s", c.Class)
		}
		if filepath.Base(c.Path) == ".claude.json" && c.Field != "primaryApiKey" {
			t.Errorf("field = %q", c.Field)
		}
	}
	if g := byProvider(r, "google"); g == nil || len(g.Copies) != 1 {
		t.Fatalf("google: transcript must not be scanned, got %+v", g)
	}
	if or := byProvider(r, "openrouter"); or == nil || len(or.Copies) != 5 {
		t.Fatalf("openrouter: %+v", or)
	}
	if ds := byProvider(r, "deepseek"); ds == nil {
		t.Fatal("deepseek key in opencode.json provider block not found")
	}
}

func TestOpenCodeV1AuthIsLive(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	os.Remove(filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	r, _ := Run(Options{Env: env(home)})
	for _, c := range byProvider(r, "anthropic").Copies {
		if filepath.Base(c.Path) == "auth.json" && c.Class != locate.Live {
			t.Fatalf("without opencode.db, auth.json is what OpenCode reads: got %s", c.Class)
		}
	}
}

func TestSymlinkCountedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	testhome.Build(home)
	dot := filepath.Join(home, "dotfiles")
	os.MkdirAll(dot, 0o755)
	os.Rename(filepath.Join(home, ".zshrc"), filepath.Join(dot, "zshrc"))
	if err := os.Symlink(filepath.Join(dot, "zshrc"), filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	r, _ := Run(Options{Env: env(home)})
	n := 0
	for _, c := range byProvider(r, "anthropic").Copies {
		if c.Tool == "shell" {
			n++
			if c.RealPath == "" {
				t.Error("symlinked rc file should report its target")
			}
		}
	}
	if n != 1 {
		t.Fatalf("~/.zshrc counted %d times", n)
	}
}

func TestFindQuery(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	r, _ := Run(Options{Env: env(home)})
	an := byProvider(r, "anthropic")
	if got := r.Find(an.Fingerprint[:4]); len(got) != 1 {
		t.Fatalf("prefix lookup: %d", len(got))
	}
	if got := r.Find(an.Hint[len(an.Hint)-4:]); len(got) != 1 {
		t.Fatalf("hint lookup: %d", len(got))
	}
}

func TestOpenCodeDBOverride(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	def := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	moved := filepath.Join(home, "elsewhere", "oc.db")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	if err := os.Rename(def, moved); err != nil {
		t.Fatal(err)
	}
	e := env(home)
	e.Getenv = func(k string) string {
		if k == "OPENCODE_DB" {
			return moved
		}
		return ""
	}
	r, _ := Run(Options{Env: e, NoWalk: true})
	var db, legacy bool
	for _, c := range byProvider(r, "anthropic").Copies {
		db = db || (c.Path == moved && c.Class == locate.Database)
		legacy = legacy || (filepath.Base(c.Path) == "auth.json" && c.Class == locate.Residue)
	}
	if !db || !legacy {
		t.Fatalf("OPENCODE_DB not honoured: db=%v legacy-auth=%v", db, legacy)
	}
}
