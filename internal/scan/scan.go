// Package scan reads the locations, finds provider keys, and groups every
// copy of the same key under one fingerprint. Key values never leave this
// package except through Key.Secret, which rotate and check use in memory.
package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Blackman99/keyroll/internal/credstore"
	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/provider"
)

// Copy is one place a key sits.
type Copy struct {
	Path     string       `json:"path"`
	Display  string       `json:"display"`
	Tool     string       `json:"tool"`
	Class    locate.Class `json:"class"`
	Field    string       `json:"field,omitempty"`
	Lines    []int        `json:"lines,omitempty"`
	Count    int          `json:"count"`
	Note     string       `json:"note,omitempty"`
	Manual   string       `json:"manual,omitempty"`
	RealPath string       `json:"real_path,omitempty"` // set when Path is a symlink
	GitRepo  string       `json:"git_repo,omitempty"`  // file lives inside this git work tree
}

// Key is one distinct key value and all of its copies.
type Key struct {
	Fingerprint string        `json:"fingerprint"` // first 8 hex of sha256(value)
	Hint        string        `json:"hint"`        // issuer prefix + last 4, as dashboards show it
	Provider    string        `json:"provider"`
	ProviderNam string        `json:"provider_name"`
	Kind        provider.Kind `json:"kind"`
	Copies      []Copy        `json:"copies"`
	secret      string
}

// Secret returns the key value. Only check and rotate call it.
func (k *Key) Secret() string { return k.secret }

// Prov returns the provider entry.
func (k *Key) Prov() *provider.Provider { return provider.ByID(k.Provider) }

// Live, Residue and Database count copies by class.
func (k *Key) Count(c locate.Class) int {
	n := 0
	for _, cp := range k.Copies {
		if cp.Class == c {
			n++
		}
	}
	return n
}

// Report is the result of one scan.
type Report struct {
	Keys         []*Key        `json:"keys"`
	FilesScanned int           `json:"files_scanned"`
	DirsWalked   int           `json:"dirs_walked"`
	Unreadable   []string      `json:"unreadable,omitempty"`
	Roots        []string      `json:"roots"`
	Elapsed      time.Duration `json:"-"`
	ElapsedMS    int64         `json:"elapsed_ms"`
	Home         string        `json:"-"`
	// CredentialStore lists OS credential-store entries known AI tools keep
	// keys in, by name only (nil when not asked, e.g. --no-credstore).
	CredentialStore *credstore.Result `json:"credential_store,omitempty"`
}

// Copies is the total number of copies.
func (r *Report) Copies() int {
	n := 0
	for _, k := range r.Keys {
		n += len(k.Copies)
	}
	return n
}

// Tools is the number of distinct tools holding at least one key.
func (r *Report) Tools() int {
	m := map[string]bool{}
	for _, k := range r.Keys {
		for _, c := range k.Copies {
			m[c.Tool] = true
		}
	}
	return len(m)
}

// Options control a scan.
type Options struct {
	Env      locate.Env
	Roots    []string // project roots to walk for .env files; default: home
	Depth    int      // walk depth (default 6)
	MaxDirs  int      // stop walking after this many directories (default 200000)
	Extra    []string // extra files to scan (as live copies)
	NoWalk   bool
	Provider string // only keep keys of this provider id
	// Stores, when set, asks the OS credential store which known entries
	// exist (names only, never values).
	Stores func() credstore.Result
}

// Run scans the machine.
func Run(o Options) (*Report, error) {
	t0 := time.Now()
	if o.Env.Home == "" {
		o.Env = locate.FromOS()
	}
	if o.Depth == 0 {
		o.Depth = 6
	}
	if o.MaxDirs == 0 {
		o.MaxDirs = 200000
	}
	if len(o.Roots) == 0 {
		o.Roots = []string{o.Env.Home}
	}
	locs := locate.Known(o.Env)
	walked := 0
	if !o.NoWalk {
		var w []locate.Location
		w, walked = locate.Walk(o.Roots, locate.HomeSkips(o.Env), o.Depth, o.MaxDirs)
		locs = append(locs, w...)
	}
	for _, x := range o.Extra {
		abs, _ := filepath.Abs(x)
		locs = append(locs, locate.Location{Path: abs, Tool: "extra file", Class: locate.Live})
	}

	r := &Report{Roots: o.Roots, DirsWalked: walked, Home: o.Env.Home}
	byVal := map[string]*Key{}
	seenReal := map[string]bool{}
	for _, l := range locs {
		fi, err := os.Lstat(l.Path)
		if err != nil || fi.IsDir() {
			continue
		}
		real := l.Path
		if fi.Mode()&os.ModeSymlink != 0 {
			if rp, err := filepath.EvalSymlinks(l.Path); err == nil {
				real = rp
			} else {
				continue
			}
			if st, err := os.Stat(real); err != nil || st.IsDir() {
				continue
			}
		}
		if seenReal[real] {
			continue
		}
		seenReal[real] = true
		hits, err := scanFile(real, l)
		if err != nil {
			r.Unreadable = append(r.Unreadable, display(o.Env.Home, l.Path))
			continue
		}
		r.FilesScanned++
		repo, repoDone := "", false
		for _, h := range hits {
			if !repoDone {
				repo, repoDone = gitRepo(real), true
			}
			if o.Provider != "" && h.m.Provider.ID != o.Provider {
				continue
			}
			k := byVal[h.m.Value]
			if k == nil {
				sum := sha256.Sum256([]byte(h.m.Value))
				k = &Key{Fingerprint: hex.EncodeToString(sum[:4]), Hint: provider.Hint(h.m.Value),
					Provider: h.m.Provider.ID, ProviderNam: h.m.Provider.Name, Kind: h.m.Kind, secret: h.m.Value}
				byVal[h.m.Value] = k
			}
			c := k.copyFor(l.Path)
			if c == nil {
				k.Copies = append(k.Copies, Copy{Path: l.Path, Display: display(o.Env.Home, l.Path), Tool: l.Tool,
					Class: l.Class, Note: l.Note, Manual: l.Manual, GitRepo: repo})
				c = &k.Copies[len(k.Copies)-1]
				if real != l.Path {
					c.RealPath = real
				}
			}
			c.Count++
			if h.line > 0 && l.Class != locate.Database && len(c.Lines) < 20 {
				c.Lines = append(c.Lines, h.line)
			}
			if c.Field == "" {
				c.Field = h.field
			}
		}
	}
	for _, k := range byVal {
		sort.SliceStable(k.Copies, func(i, j int) bool { return classRank(k.Copies[i].Class) < classRank(k.Copies[j].Class) })
		r.Keys = append(r.Keys, k)
	}
	sort.Slice(r.Keys, func(i, j int) bool {
		a, b := r.Keys[i], r.Keys[j]
		if len(a.Copies) != len(b.Copies) {
			return len(a.Copies) > len(b.Copies)
		}
		return a.Fingerprint < b.Fingerprint
	})
	if o.Stores != nil {
		cs := o.Stores().Filter(o.Provider)
		r.CredentialStore = &cs
	}
	r.Elapsed = time.Since(t0)
	r.ElapsedMS = r.Elapsed.Milliseconds()
	return r, nil
}

func classRank(c locate.Class) int {
	switch c {
	case locate.Live:
		return 0
	case locate.Database:
		return 1
	}
	return 2
}

func (k *Key) copyFor(path string) *Copy {
	for i := range k.Copies {
		if k.Copies[i].Path == path {
			return &k.Copies[i]
		}
	}
	return nil
}

// Find returns the key whose fingerprint (or a unique prefix of it, or its
// hint suffix) matches q.
func (r *Report) Find(q string) []*Key {
	q = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(q)), "#")
	var out []*Key
	for _, k := range r.Keys {
		if strings.HasPrefix(k.Fingerprint, q) || (len(q) >= 4 && strings.HasSuffix(strings.ToLower(k.Hint), q)) {
			out = append(out, k)
		}
	}
	return out
}

type hit struct {
	m     provider.Match
	line  int
	field string
}

const chunk = 1 << 20
const overlap = 1024

func scanFile(path string, l locate.Location) ([]hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []hit
	buf := make([]byte, chunk+overlap)
	carry := 0
	base := int64(0) // file offset of buf[0]
	line := 1        // line number at buf[0]
	seen := map[int64]bool{}
	for {
		n, rerr := io.ReadFull(f, buf[carry:])
		data := buf[:carry+n]
		if len(data) == 0 {
			break
		}
		if l.Class != locate.Database && bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 && base == 0 && !l.Stream {
			return nil, nil // binary file we did not expect: skip quietly
		}
		for _, m := range provider.Find(data) {
			abs := base + int64(m.Start)
			if seen[abs] {
				continue
			}
			seen[abs] = true
			ln := line + bytes.Count(data[:m.Start], []byte{'\n'})
			out = append(out, hit{m: m, line: ln, field: fieldName(data, m.Start, l)})
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return out, rerr
		}
		keep := overlap
		if keep > len(data) {
			keep = len(data)
		}
		line += bytes.Count(data[:len(data)-keep], []byte{'\n'})
		base += int64(len(data) - keep)
		copy(buf, data[len(data)-keep:])
		carry = keep
	}
	return out, nil
}

var (
	fieldRe  = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_.\-:]*)["']?\s*(?:[:=]|\s)\s*["']?$`)
	objectRe = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:\s*\{[^{}]*$`)
)

func fieldName(data []byte, at int, l locate.Location) string {
	if l.Class == locate.Database {
		return ""
	}
	ls := bytes.LastIndexByte(data[:at], '\n') + 1
	prefix := string(data[ls:at])
	if l.Tool == "shell history" {
		return ""
	}
	m := fieldRe.FindStringSubmatch(prefix)
	if m == nil {
		return ""
	}
	name := strings.TrimPrefix(m[1], "$env:")
	name = strings.TrimSuffix(name, ":")
	if name == "export" || name == "set" || name == "-gx" || name == "-Ux" {
		return ""
	}
	if strings.HasPrefix(name, "SETUVAR") {
		return ""
	}
	if name == "key" || name == "apiKey" || name == "api_key" || name == "token" || name == "value" {
		back := at - 400
		if back < 0 {
			back = 0
		}
		if om := objectRe.FindStringSubmatch(string(data[back:ls])); om != nil {
			return om[1] + "." + name
		}
	}
	return name
}

func display(home, p string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(filepath.Join("~", rel))
		}
	}
	return p
}

// gitRepo returns the work tree root if path is a file git tracks (dotfile
// repos, a committed .env): the old key stays in that history even after
// rotation, so it must be revoked, not just replaced.
func gitRepo(path string) string {
	root := gitRoot(path)
	if root == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", "--", filepath.ToSlash(rel))
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	if cmd.Run() != nil {
		return ""
	}
	return root
}

func gitRoot(path string) string {
	d := filepath.Dir(path)
	for i := 0; i < 12; i++ {
		if st, err := os.Stat(filepath.Join(d, ".git")); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return d
		}
		nd := filepath.Dir(d)
		if nd == d {
			break
		}
		d = nd
	}
	return ""
}
