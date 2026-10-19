/* keyroll landing: a cord switchboard that replays what the CLI prints.
   One key = one trunk line (a plug on the keyshelf). Every copy of it on disk
   = a cord patched into a jack labelled with the file. No physics: cords are
   cubic beziers drawn with stroke-dashoffset, and the plug head rides the
   path with getPointAtLength. */
(() => {
  "use strict";
  const NS = "http://www.w3.org/2000/svg";
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const css = n => getComputedStyle(document.documentElement).getPropertyValue(n).trim();

  const KEYS = [
    { id: "A", pid: "anthropic", name: "Anthropic", hint: "sk-ant-…M1AA", fp: "38bb1575", cord: "--cord-a", alive: false, detail: "refused, HTTP 401" },
    { id: "R", pid: "openrouter", name: "OpenRouter", hint: "sk-or-v1-…7bdc", fp: "8e460503", cord: "--cord-r", alive: true, detail: "alive · no spend limit, used $412.20" },
    { id: "O", pid: "openai", name: "OpenAI", hint: "sk-proj-…5Gt_", fp: "268a465f", cord: "--cord-o", alive: true, detail: "alive · accepted (200)" },
    { id: "D", pid: "deepseek", name: "DeepSeek", hint: "sk-…81e4", fp: "93530c02", cord: "--cord-v", alive: true, detail: "alive" },
    { id: "G", pid: "google", name: "Google AI Studio", short: "Google", hint: "AIza…qz8j", fp: "ab770573", cord: "--cord-g", alive: true, detail: "alive · accepted (200)" },
  ];
  const NEW = { id: "N", name: "new Anthropic key", short: "NEW", hint: "sk-ant-…kwAA", cord: "--cord-new" };
  const L = "live", X = "leftover", DB = "database", KC = "credential store";
  // Same 19 copies the demo home in the repo produces (internal/testhome), plus
  // one simulated macOS Keychain entry: keyroll sees its name, never its value,
  // so it is not a copy and never gets a cord.
  const STRIPS = [
    { t: "CLAUDE CODE", j: [
      // the store entry sits on top: cords come up from below, so none runs across it
      { f: "Keychain", k: "A", c: KC, store: "macOS Keychain", p: 'macOS Keychain · "Claude Code"', tool: "Claude Code", holds: "Anthropic API key · changed 2026-10-03",
        man: "run `claude`, /logout, then /login with your Console account", ticket: "claude → /logout, then /login" },
      { f: ".claude.json", k: "A", c: L, p: "~/.claude.json:4", fld: "primaryApiKey", tool: "Claude Code" } ] },
    { t: "CODEX · LLM", j: [
      { f: ".codex/auth.json", s: "codex auth", k: "O", c: L, p: "~/.codex/auth.json:3", fld: "OPENAI_API_KEY", tool: "Codex" },
      { f: "llm keys.json", s: "llm keys", k: "O", c: L, p: "~/.config/io.datasette.llm/keys.json:3", fld: "openai", tool: "llm" } ] },
    { t: "OPENCODE", j: [
      { f: "opencode.db", k: "A", c: DB, p: "~/.local/share/opencode/opencode.db", tool: "OpenCode", man: "opencode auth login anthropic" },
      { f: "opencode.db", k: "R", c: DB, p: "~/.local/share/opencode/opencode.db", tool: "OpenCode", man: "opencode auth login openrouter" },
      { f: "opencode.json", s: "opencode.json", k: "D", c: L, p: "~/.config/opencode/opencode.json:6", fld: "options.apiKey", tool: "OpenCode" } ] },
    { t: "CLINE · GEMINI", ts: "CLINE·GEMINI", j: [
      { f: "cline secrets.json", s: "cline", k: "R", c: L, p: "~/.cline/data/secrets.json:2", fld: "openRouterApiKey", tool: "Cline" },
      { f: ".gemini/.env", s: "gemini .env", k: "G", c: L, p: "~/.gemini/.env:1", fld: "GEMINI_API_KEY", tool: "Gemini CLI" } ] },
    { t: "SHELL", j: [
      { f: ".zshrc  line 6", s: ".zshrc :6", k: "A", c: L, p: "~/.zshrc:6", fld: "ANTHROPIC_API_KEY", tool: "shell" },
      { f: ".zshrc  line 7", s: ".zshrc :7", k: "R", c: L, p: "~/.zshrc:7", fld: "OPENROUTER_API_KEY", tool: "shell" } ] },
    { t: "PROJECT .ENV", ts: "PROJECTS", j: [
      { f: "shop/.env", k: "A", c: L, p: "~/code/shop/.env:2", fld: "ANTHROPIC_API_KEY", tool: "project .env" },
      { f: "shop/.env", k: "O", c: L, p: "~/code/shop/.env:3", fld: "OPENAI_API_KEY", tool: "project .env" },
      { f: "agent-bot/.dev.vars", s: ".dev.vars", k: "R", c: L, p: "~/code/agent-bot/.dev.vars:1", fld: "OPENROUTER_API_KEY", tool: "project .env" },
      { f: "shop/.env.example", s: ".env.example", k: "A", c: X, p: "~/code/shop/.env.example:2", tool: "project .env", why: "template file: a real key should not be here" } ] },
    { t: "STALE FILES", ts: "STALE", j: [
      { f: "backup.…0211", k: "A", c: X, p: "~/.claude/backups/.claude.json.backup.1760650211:4", tool: "Claude Code", why: "automatic backup of ~/.claude.json" },
      { f: "backup.…6622", k: "A", c: X, p: "~/.claude/backups/.claude.json.backup.1760736622:4", tool: "Claude Code", why: "automatic backup of ~/.claude.json" },
      { f: "opencode auth.json", s: "oc auth.json", k: "A", c: X, p: "~/.local/share/opencode/auth.json:4", tool: "OpenCode", why: "legacy file: OpenCode v2 imported it into opencode.db and no longer reads it" },
      { f: "opencode auth.json", s: "oc auth.json", k: "R", c: X, p: "~/.local/share/opencode/auth.json:8", tool: "OpenCode", why: "legacy file: OpenCode v2 imported it into opencode.db and no longer reads it" } ] },
    { t: "HISTORY", j: [
      { f: ".zsh_history", k: "A", c: X, p: "~/.zsh_history:2", tool: "shell history", why: "you once typed it in a command" } ] },
  ];
  const keyOf = id => id === "N" ? NEW : KEYS.find(k => k.id === id);
  const jacks = [];
  STRIPS.forEach((s, si) => s.j.forEach((j, ji) => jacks.push(Object.assign(j, { si, ji }))));
  const files = jacks.filter(j => !j.store); // what keyroll counts as copies
  const stores = jacks.filter(j => j.store);

  // CLI output, verbatim from keyroll on the demo home (check: simulated answers).
  const LOG = {
    idle: `<span class="k-dim">$</span> keyroll
<span class="k-dim">pull a lever key, or pick a plug on the keyshelf</span>`,
    A: null, // built from jacks
    check: `<span class="k-dim">$</span> keyroll check
<span class="k-b">keyroll check</span>  5 keys · 5 requests <span class="k-dim">· each key went only to its own issuer</span>

  <span class="k-r">✕ <b>REJECTED</b></span>  Anthropic         sk-ant-…M1AA     <span class="k-c">#38bb1575</span>  9 copies   <span class="k-dim">refused, HTTP 401</span>
  <span class="k-g">● <b>ALIVE</b></span>     OpenRouter        sk-or-v1-…7bdc   <span class="k-c">#8e460503</span>  5 copies   <span class="k-y">no spend limit on this key, used $412.20</span>
  <span class="k-g">● <b>ALIVE</b></span>     OpenAI            sk-proj-…5Gt_    <span class="k-c">#268a465f</span>  3 copies   <span class="k-dim">accepted (200)</span>
  <span class="k-g">● <b>ALIVE</b></span>     DeepSeek          sk-…81e4         <span class="k-c">#93530c02</span>  1 copy     <span class="k-dim">accepted (200)</span>
  <span class="k-g">● <b>ALIVE</b></span>     Google AI Studio  AIza…qz8j        <span class="k-c">#ab770573</span>  1 copy     <span class="k-dim">accepted (200)</span>

  <span class="k-g">ALIVE</span><span class="k-dim">    the issuer accepts it: whoever holds a copy can spend on it.</span>
  <span class="k-r">REJECTED</span><span class="k-dim"> the issuer refuses it: revoked, deleted or mistyped (issuers answer all three the same
           way). A tool still using it fails with 401 / "invalid x-api-key" / "key expired": that is the
           issuer, not a bug in your code.</span>

  <span class="k-r">!</span> Anthropic sk-ant-…M1AA is refused, but Claude Code, shell, project .env and OpenCode still use
    it: their next request fails with HTTP 401.
    <span class="k-b">keyroll rotate 38bb1575</span>  <span class="k-dim">(paste the new key once)</span>
  <span class="k-y">·</span> OpenRouter sk-or-v1-…7bdc still works and sits in 5 places. It has no spend limit. If it was ever
    pasted somewhere you no longer trust (a hosted console, a chat), rotate it:
    <span class="k-b">keyroll rotate 8e460503</span>`,
    rotate: `<span class="k-dim">$</span> keyroll rotate 38bb1575
<span class="k-dim">paste the new Anthropic key (input hidden), then Enter:</span>
<span class="k-b">keyroll rotate</span>  Anthropic sk-ant-…M1AA <span class="k-c">#38bb1575</span>  →  new sk-ant-…kwAA <span class="k-g">(works ✓)</span>

  <span class="k-g">write</span>    Claude Code     ~/.claude.json:4                                    <span class="k-dim">primaryApiKey</span>
  <span class="k-g">write</span>    shell           ~/.zshrc:6                                          <span class="k-dim">ANTHROPIC_API_KEY</span>
  <span class="k-g">write</span>    project .env    ~/code/shop/.env:2                                  <span class="k-dim">ANTHROPIC_API_KEY</span>
  <span class="k-bl">by hand</span>  OpenCode        ~/.local/share/opencode/opencode.db                 <span class="k-dim">opencode auth login anthropic</span>
  <span class="k-y">scrub</span>    OpenCode        ~/.local/share/opencode/auth.json:4                 <span class="k-dim">old key removed, new not added</span>
  <span class="k-y">scrub</span>    shell history   ~/.zsh_history:2                                    <span class="k-dim">old key removed, new not added</span>
  <span class="k-y">scrub</span>    Claude Code     ~/.claude/backups/.claude.json.backup.1760650211:4  <span class="k-dim">old key removed, new not added</span>
  <span class="k-y">scrub</span>    Claude Code     ~/.claude/backups/.claude.json.backup.1760736622:4  <span class="k-dim">old key removed, new not added</span>
  <span class="k-y">scrub</span>    project .env    ~/code/shop/.env.example:2                          <span class="k-dim">old key removed, new not added</span>

  <span class="k-bl">credential store</span> (macOS Keychain) may hold this key<span class="k-dim">: names only, keyroll cannot see the values</span>
  <span class="k-bl">by hand</span>  Claude Code     "Claude Code"  <span class="k-dim">Anthropic API key</span>
                           <span class="k-bl">→ run \`claude\`, /logout, then /login with your Console account</span>

  <span class="k-g">✓</span> 8 files changed · originals (with the OLD key) in ~/.keyroll/backups/20261018-101320-38bb1575

  <span class="k-b">now</span>
  1. OpenCode: opencode auth login anthropic (then: opencode auth list)
  2. Claude Code in the macOS Keychain ("Claude Code"), if it holds this key: run \`claude\`, /logout,
     then /login with your Console account
  3. give the new key a spend limit: https://console.anthropic.com/settings/limits
  4. restart tools that keep the key in memory (a running Claude Code, Codex or OpenCode session, an
     open shell: \`exec $SHELL\`)
<span class="k-dim">     once it is revoked, delete the backups: rm -rf ~/.keyroll/backups/20261018-101320-38bb1575</span>

  re-scanned: the old key sk-ant-…M1AA was in 9 places, now 1 (OpenCode database: see step 1)

  <span class="k-g">✓</span> old key sk-ant-…M1AA: Anthropic now <span class="k-g">REFUSES</span> it (refused, HTTP 401). It is revoked, so the copies
    you did not find are harmless.`,
  };
  const pad = (s, n) => s + " ".repeat(Math.max(1, n - s.length));
  function mapLog(k) {
    const mine = files.filter(j => j.k === k.id);
    const n = c => mine.filter(j => j.c === c).length;
    const parts = [];
    if (n(L)) parts.push(`${n(L)} live`);
    if (n(DB)) parts.push(`${n(DB)} database`);
    if (n(X)) parts.push(`${n(X)} leftover${n(X) > 1 ? "s" : ""}`);
    const order = [L, DB, X];
    const rows = mine.slice().sort((a, b) => order.indexOf(a.c) - order.indexOf(b.c));
    const w = Math.max(...rows.map(j => j.p.length)) + 2;
    const lines = rows.map(j => {
      const cls = j.c === L ? "k-b" : j.c === DB ? "k-bl" : "k-dim";
      let line = `      <span class="${cls}">${pad(j.c, 10)}</span>${pad(j.tool, 17)}${j.fld ? pad(j.p, w) + `<span class="k-dim">${j.fld}</span>` : j.p}`;
      if (j.man) line += `\n${" ".repeat(33)}<span class="k-bl">→ ${j.man}</span>  <span class="k-dim">(then: opencode auth list)</span>`;
      if (j.why) line += `\n${" ".repeat(33)}<span class="k-dim">${j.why}</span>`;
      return line;
    });
    const live = n(L) + n(DB);
    const tools = new Set(mine.map(j => j.tool)).size;
    const st = stores.filter(j => j.k === k.id);
    const store = st.length ? `

  <span class="k-bl">◇</span> <span class="k-b">credential store</span>  ${pad(st[0].store, 15)}  ${st.length} ${st.length === 1 ? "entry" : "entries"}  <span class="k-dim">by name only: keyroll never reads these values</span>
${st.map(j => `      <span class="k-bl">by hand</span>   ${pad(j.tool, 17)}"${j.p.split('"')[1]}"  <span class="k-dim">${j.holds}</span>
${" ".repeat(33)}<span class="k-bl">→ ${j.man}</span>`).join("\n")}` : "";
    return `<span class="k-dim">$</span> keyroll --provider ${k.pid}
<span class="k-b">keyroll</span>  1 key · ${mine.length} ${mine.length > 1 ? "copies" : "copy"} · ${tools} ${tools > 1 ? "tools" : "tool"} <span class="k-dim">· offline, nothing sent</span>

  ${live ? "●" : "○"} ${pad(k.name, 18)}${pad(k.hint, 17)}<span class="k-c">#${k.fp}</span>  ${pad(mine.length + (mine.length > 1 ? " copies" : " copy"), 10)}<span class="k-dim">${parts.join(" · ")}</span>
${lines.join("\n")}${store}

<span class="k-dim">  asked the macOS Keychain whether 13 known entries exist (names only, never values)</span>`;
  }

  // ── geometry ────────────────────────────────────────────────────────
  const stage = document.getElementById("stage");
  const logEl = document.getElementById("log");
  const meter = document.getElementById("meter");
  const tip = document.getElementById("detail");
  const levers = { map: document.getElementById("lv-map"), check: document.getElementById("lv-check"), rotate: document.getElementById("lv-rotate") };
  let G, svg, layers, state;

  function el(tag, attrs, parent) {
    const e = document.createElementNS(NS, tag);
    for (const a in attrs) e.setAttribute(a, attrs[a]);
    if (parent) parent.appendChild(e);
    return e;
  }

  function layout() {
    const narrow = stage.clientWidth < 700;
    const W = narrow ? 480 : 1200, cols = narrow ? 4 : 8, pad = narrow ? 16 : 30;
    const colW = (W - pad * 2) / cols, rowH = narrow ? 64 : 72;
    const bands = Math.ceil(STRIPS.length / cols);
    const bandH = [];
    for (let b = 0; b < bands; b++) bandH.push(52 + rowH * Math.max(...STRIPS.slice(b * cols, b * cols + cols).map(s => s.j.length)) + (narrow ? 30 : 0)); // narrow: room for the by-hand ticket rail
    const panelTop = 10, panelH = bandH.reduce((a, b) => a + b, 0) + 16;
    const shelfTop = panelTop + panelH + (narrow ? 26 : 34), shelfH = narrow ? 128 : 150;
    const H = shelfTop + shelfH + 6;
    const pos = {};
    let y0 = panelTop + 12;
    STRIPS.forEach((s, si) => {
      const b = Math.floor(si / cols), c = si % cols;
      const by = panelTop + 12 + bandH.slice(0, b).reduce((a, v) => a + v, 0);
      s.x = pad + c * colW; s.y = by; s.w = colW;
      s.j.forEach((j, ji) => { j.x = s.x + colW / 2; j.y = by + 46 + ji * rowH; });
    });
    // keyshelf: plugs on the left, lever keys (HTML) on the right
    const plugGap = narrow ? 82 : 118, plugX0 = narrow ? 44 : 84;
    KEYS.forEach((k, i) => { k.x = plugX0 + i * plugGap; k.y = shelfTop + (narrow ? 50 : 58); });
    NEW.x = plugX0 + KEYS.length * plugGap + (narrow ? -8 : 10); NEW.y = KEYS[0].y;
    if (narrow) { NEW.x = W - 40; }
    return { narrow, W, H, cols, pad, colW, rowH, panelTop, panelH, shelfTop, shelfH, y0, pos };
  }

  function draw() {
    G = layout();
    stage.textContent = "";
    svg = el("svg", { viewBox: `0 0 ${G.W} ${G.H}`, role: "img", "aria-label": "Switchboard: each plug on the shelf is one API key; each jack above is a file on disk that holds a copy." }, stage);
    const defs = el("defs", {}, svg);
    const f = el("filter", { id: "ham", x: 0, y: 0, width: 1, height: 1 }, defs);
    el("feTurbulence", { type: "fractalNoise", baseFrequency: ".9", numOctaves: 2, seed: 3, result: "n" }, f);
    el("feColorMatrix", { in: "n", values: "0 0 0 0 1  0 0 0 0 1  0 0 0 0 1  0 0 0 .05 0" }, f);
    el("feComposite", { in2: "SourceGraphic", operator: "in" }, f);

    // jack panel (bakelite)
    const panel = el("g", {}, svg);
    el("rect", { x: G.pad - 14, y: G.panelTop, width: G.W - 2 * G.pad + 28, height: G.panelH, rx: 9, fill: css("--bakelite") }, panel);
    el("rect", { x: G.pad - 14, y: G.panelTop, width: G.W - 2 * G.pad + 28, height: G.panelH, rx: 9, fill: "#fff", filter: "url(#ham)" }, panel);
    STRIPS.forEach(s => {
      el("rect", { x: s.x + 6, y: s.y, width: s.w - 12, height: 24, rx: 2, fill: css("--strip") }, panel);
      const t = el("text", { x: s.x + s.w / 2, y: s.y + 16, "text-anchor": "middle", fill: css("--bakelite"), "font-family": "Libre Franklin, sans-serif", "font-weight": 800, "font-size": G.narrow ? 9.5 : 10.5, "letter-spacing": "1.3" }, panel);
      t.textContent = G.narrow && s.ts ? s.ts : s.t;
      const room = s.w - 34;
      if (t.getComputedTextLength() > room) { t.setAttribute("textLength", room); t.setAttribute("lengthAdjust", "spacingAndGlyphs"); }
      // screws at strip ends
      [s.x + 12, s.x + s.w - 12].forEach(x => el("circle", { cx: x, cy: s.y + 12, r: 2.2, fill: "#9aa29f" }, panel));
      s.j.forEach(j => drawJack(panel, j));
    });

    // keyshelf (hammertone, in perspective)
    const shelf = el("g", {}, svg);
    const t = G.shelfTop, b = t + G.shelfH, inset = G.narrow ? 10 : 22;
    el("path", { d: `M${G.pad - 14 + inset},${t} H${G.W - G.pad + 14 - inset} L${G.W - 2},${b} H2 Z`, fill: css("--hammer-dk") }, shelf);
    el("path", { d: `M${G.pad - 14 + inset},${t} H${G.W - G.pad + 14 - inset}`, stroke: css("--hammer-hi"), "stroke-width": 2 }, shelf);
    el("path", { d: `M${G.pad - 14 + inset},${t} H${G.W - G.pad + 14 - inset} L${G.W - 2},${b} H2 Z`, fill: "#fff", filter: "url(#ham)" }, shelf);
    layers = { cords: el("g", {}, svg), heads: el("g", {}, svg), labels: el("g", { "pointer-events": "none" }, svg), shelfTop: el("g", {}, svg), tags: el("g", {}, svg) };
    // file labels sit above the cords on a bakelite chip, so a cord never runs through a filename
    jacks.forEach(j => {
      layers.labels.appendChild(j.chip); layers.labels.appendChild(j.label); layers.labels.appendChild(j.tick);
      const w = j.label.getComputedTextLength();
      j.chip.setAttribute("x", j.x - w / 2 - 4); j.chip.setAttribute("width", w + 8);
    });
    KEYS.concat([NEW]).forEach(k => drawPlug(k));
    placeLevers();
    restore();
  }

  function drawJack(parent, j) {
    const g = el("g", { class: "jack" + (j.store ? " store" : ""), tabindex: 0, role: "button", "aria-label": j.store ? `${j.p}: credential-store entry, name only` : `${j.p} (${j.c})` }, parent);
    el("rect", { x: j.x - 34, y: j.y - 12, width: 68, height: G.rowH - 4, fill: "transparent" }, g);
    j.chip = el("rect", { x: j.x - 30, y: j.y - (G.narrow ? 10 : 11), width: 60, height: G.narrow ? 13 : 14, rx: 2, fill: css("--bakelite") }, g);
    const lab = el("text", { x: j.x, y: j.y, "text-anchor": "middle", fill: css("--strip"), "font-family": "Chivo Mono, monospace", "font-size": G.narrow ? 9 : 10 }, g);
    lab.textContent = G.narrow && j.s ? j.s : j.f;
    j.label = lab;
    // which trunk owns this jack: a short cord-colour tick under the label
    // credential-store entry: a dashed tick, it is only probably this trunk's
    j.tick = j.store
      ? el("path", { d: `M${j.x - 9},${j.y + 6.5} h18`, stroke: css(keyOf(j.k).cord), "stroke-width": 3, "stroke-dasharray": "4 3", opacity: .9 }, g)
      : el("rect", { x: j.x - 9, y: j.y + 5, width: 18, height: 3, rx: 1.5, fill: css(keyOf(j.k).cord), opacity: .9 }, g);
    j.jy = j.y + 40;
    j.lamp = el("circle", { class: "lamp", cx: j.x, cy: j.y + 19, r: 6.5, fill: "#2a2822", stroke: "#5c625f", "stroke-width": 1.5 }, g);
    if (j.store) j.halo = el("circle", { class: "halo", cx: j.x, cy: j.jy, r: 14.5, fill: "none", stroke: css("--ring"), "stroke-width": 2, "stroke-dasharray": "3 3.6", opacity: 0 }, g);
    el("circle", { class: "ringo", cx: j.x, cy: j.jy, r: 10, fill: css("--nickel") }, g);
    if (j.store) {
      // blanked off: a bakelite diamond cover over the hole. No plug fits; it is a by-hand step.
      el("rect", { x: j.x - 5, y: j.jy - 5, width: 10, height: 10, rx: 1.2, fill: css("--bakelite"), transform: `rotate(45 ${j.x} ${j.jy})` }, g);
      el("rect", { x: j.x - 2, y: j.jy - 2, width: 4, height: 4, fill: css("--nickel"), transform: `rotate(45 ${j.x} ${j.jy})` }, g);
    } else el("circle", { cx: j.x, cy: j.jy, r: 4.6, fill: "#050504" }, g);
    const show = () => showTip(j);
    g.addEventListener("mouseenter", show); g.addEventListener("focus", show);
    g.addEventListener("mouseleave", hideTip); g.addEventListener("blur", hideTip);
    g.addEventListener("click", () => { pick(j.k === "N" ? "A" : j.k); show(); });
    g.addEventListener("keydown", e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); g.dispatchEvent(new Event("click")); } });
  }

  function drawPlug(k) {
    const g = el("g", { class: "plug", tabindex: k === NEW ? -1 : 0, role: "button", "aria-label": `${k.name} ${k.hint}` }, layers.shelfTop);
    k.g = g;
    el("ellipse", { cx: k.x, cy: k.y + 2, rx: 13, ry: 5, fill: "#3d4d43" }, g);
    k.parked = el("g", {}, g);
    el("rect", { x: k.x - 7, y: k.y - 30, width: 14, height: 30, rx: 4, fill: css("--bakelite"), class: "plugcap" }, k.parked);
    el("rect", { x: k.x - 7, y: k.y - 12, width: 14, height: 5, fill: css(k.cord) }, k.parked);
    el("rect", { x: k.x - 2.2, y: k.y - 42, width: 4.4, height: 13, rx: 2, fill: css("--nickel") }, k.parked);
    k.trunk = el("circle", { class: "lamp", cx: k.x + (G.narrow ? 0 : 22), cy: G.narrow ? k.y + 14 : k.y - 24, r: G.narrow ? 4.5 : 5.5, fill: "#2a2822", stroke: "#3d4d43", "stroke-width": 1.5 }, g);
    const name = el("text", { x: k.x, y: k.y + (G.narrow ? 32 : 24), "text-anchor": "middle", fill: css("--bakelite"), "font-family": "Libre Franklin, sans-serif", "font-weight": 800, "font-size": G.narrow ? 10 : 12.5 }, g);
    name.textContent = G.narrow ? (k.short || k.name) : k.name;
    const hint = el("text", { x: k.x, y: k.y + (G.narrow ? 44 : 39), "text-anchor": "middle", fill: css("--bakelite"), "font-family": "Chivo Mono, monospace", "font-size": G.narrow ? 8 : 10.5 }, g);
    hint.textContent = k.hint;
    k.status = el("text", { x: k.x, y: k.y + (G.narrow ? 55 : 54), "text-anchor": "middle", fill: css("--bakelite"), "font-family": "Libre Franklin, sans-serif", "font-weight": 700, "font-size": G.narrow ? 7.5 : 9.5, "letter-spacing": 1 }, g);
    if (k === NEW) { g.style.opacity = 0; return; }
    g.addEventListener("click", () => pick(k.id));
    g.addEventListener("keydown", e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); g.dispatchEvent(new Event("click")); } });
  }

  function placeLevers() {
    if (G.narrow) return; // CSS puts them under the board
    const box = document.querySelector(".levers");
    const sb = G.shelfTop + G.shelfH * .5;
    box.style.top = (sb / G.H * 100) + "%";
    box.style.bottom = "auto";
  }

  // ── cords ───────────────────────────────────────────────────────────
  function cordPath(k, j) {
    const x0 = k.x, y0 = k.y - 2, x1 = j.x, y1 = j.jy + 34;
    const sag = Math.min(150, (y0 - y1) * .55);
    return `M${x0},${y0} C${x0},${y0 - sag} ${x1},${y1 + sag * .9} ${x1},${y1}`;
  }
  function plugHead(color) {
    const g = el("g", {});
    el("rect", { x: -7, y: -4, width: 14, height: 26, rx: 4, fill: css("--bakelite") }, g);
    el("rect", { x: -7, y: 14, width: 14, height: 4, fill: color }, g);
    el("rect", { x: -2.4, y: -16, width: 4.8, height: 13, rx: 2, fill: css("--nickel") }, g);
    return g;
  }
  function cord(k, j, ms = 700) {
    const color = css(k.cord);
    const g = el("g", {}, layers.cords);
    const d = cordPath(k, j);
    const base = el("path", { d, class: "cord", stroke: color, "stroke-width": G.narrow ? 4 : 5.5 }, g);
    const braid = el("path", { d, class: "cord", stroke: k === NEW ? "#9b9789" : "#000", "stroke-opacity": k === NEW ? .55 : .28, "stroke-width": G.narrow ? 4 : 5.5, "stroke-dasharray": "1.4 2.6", opacity: 0 }, g);
    const head = plugHead(color);
    layers.heads.appendChild(head);
    const len = base.getTotalLength();
    const c = { k, j, g, base, braid, head, len, d };
    j.cord = c;
    return animate(c, 0, 1, ms).then(() => { braid.setAttribute("opacity", 1); snap(c); return c; });
  }
  function setT(c, t) {
    c.base.style.strokeDasharray = c.len;
    c.base.style.strokeDashoffset = c.len * (1 - t);
    const p = c.base.getPointAtLength(c.len * t);
    const q = c.base.getPointAtLength(Math.max(0, c.len * t - 2));
    let ang = Math.atan2(p.y - q.y, p.x - q.x) * 180 / Math.PI + 90;
    if (t === 0) ang = 0;
    // head travels tip-first along the cord; at the jack it points straight up
    c.head.lastChild.setAttribute("opacity", 1);
    c.head.setAttribute("transform", `translate(${p.x},${p.y - 22}) rotate(${t >= 1 ? 0 : ang} 0 22)`);
  }
  // seated: the tip is inside the jack, only the handle shows under the ring
  function snap(c) { c.head.setAttribute("transform", `translate(${c.j.x},${c.j.jy + 12})`); c.head.lastChild.setAttribute("opacity", 0); }
  const ease = t => t < .5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
  function animate(c, from, to, ms) {
    if (reduce || fast || ms === 0) { setT(c, to); return Promise.resolve(); }
    return new Promise(res => {
      const t0 = performance.now();
      const step = now => {
        const u = Math.min(1, (now - t0) / ms);
        setT(c, from + (to - from) * ease(u));
        if (u < 1 && c.g.isConnected) requestAnimationFrame(step); else res();
      };
      requestAnimationFrame(step);
    });
  }
  function pull(j, ms = 520) {
    const c = j.cord; if (!c) return Promise.resolve();
    j.cord = null;
    c.braid.setAttribute("opacity", 0);
    return animate(c, 1, 0, ms).then(() => { c.g.remove(); c.head.remove(); });
  }
  function clearCords() {
    jacks.forEach(j => { if (j.cord) { j.cord.g.remove(); j.cord.head.remove(); j.cord = null; } });
    layers.tags.textContent = "";
  }
  let fast = false; // redrawing a saved state after a resize: no animation
  const wait = ms => new Promise(r => setTimeout(r, fast ? 0 : reduce ? Math.min(ms, 60) : ms));

  // ── lamps, tags ─────────────────────────────────────────────────────
  const LAMP = { off: "#2a2822" };
  function lamp(node, color, mode) {
    node.classList.remove("blink", "ringing");
    node.setAttribute("fill", color === "off" ? LAMP.off : css(color));
    if (mode) { void node.getBBox(); node.classList.add(mode); }
  }
  function busyPlug(j) {
    // make-busy plug: a stubby black plug with no cord, the jack is dead now
    const g = el("g", { transform: `translate(${j.x},${j.jy})` }, layers.tags);
    el("circle", { r: 7.5, fill: "#050504", stroke: "#5c625f", "stroke-width": 1.2 }, g);
    el("rect", { x: -5.5, y: 4, width: 11, height: 12, rx: 3, fill: "#050504", stroke: "#5c625f", "stroke-width": 1.2 }, g);
    const t = el("text", { x: j.x + 11, y: j.y + 22, fill: css("--ring"), "font-size": G.narrow ? 7 : 8.5, "font-family": "Libre Franklin, sans-serif", "font-weight": 800, "letter-spacing": 1 }, layers.tags);
    t.textContent = "SCRUBBED";
    j.label.setAttribute("opacity", .4);
    j.tick.setAttribute("opacity", 0);
    if (!reduce) g.animate([{ transform: `translate(${j.x}px,${j.jy - 14}px)`, opacity: 0 }, { transform: `translate(${j.x}px,${j.jy}px)`, opacity: 1 }], { duration: 260, easing: "cubic-bezier(.3,1.5,.5,1)" });
  }
  function ticket(j, text) {
    const w = G.narrow ? 160 : 200, x = Math.max(G.pad - 8, Math.min(j.x - w / 2, G.W - G.pad - w + 10));
    // ticket holder: the bottom rail of the band this jack sits in
    const band = Math.floor(j.si / G.cols), next = STRIPS[(band + 1) * G.cols];
    const y = next ? next.y - (G.narrow ? 34 : 28) : G.panelTop + G.panelH - (G.narrow ? 14 : 16);
    const g = el("g", { class: "ticket", transform: `translate(${x},${y}) rotate(-1.5)` }, layers.tags);
    el("rect", { width: w, height: G.narrow ? 30 : 36, rx: 2, fill: css("--strip"), stroke: css("--ring"), "stroke-width": 2 }, g);
    const a = el("text", { x: 7, y: G.narrow ? 11 : 14, "font-size": G.narrow ? 7 : 9, "font-weight": 600, fill: css("--bakelite") }, g);
    a.textContent = G.narrow ? `BY HAND · ${j.store ? "KEYCHAIN" : keyOf(j.k).name}` : j.store ? `BY HAND · Keychain "${j.p.split('"')[1]}"` : `BY HAND · ${j.f} · ${keyOf(j.k).name}`;
    const b = el("text", { x: 7, y: G.narrow ? 23 : 28, "font-size": G.narrow ? 7 : 9.5, fill: css("--bakelite") }, g);
    b.textContent = text;
    if (!reduce) g.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300 });
  }

  // ── tooltip ─────────────────────────────────────────────────────────
  function showTip(j) {
    const k = keyOf(j.k);
    if (j.store) return placeTip(j, `<b>${j.tool} · credential store</b><code>${j.p}</code><br>An entry by this name exists. keyroll asked for its name only and never reads the value, so it cannot tell whether it holds ${k.name} <span class="nw">${k.hint}</span>.<br>→ ${state.rotated ? "left for you: " : "rotate lists it as a by-hand step: "}${j.man.replace(/`/g, "")}`);
    const action = state.rotated && j.k === "A"
      ? (j.c === L ? "rewritten with the new key" : j.c === X ? "old key scrubbed, new key not added" : "left for you: " + j.man)
      : j.c === L ? "rotate rewrites this one" : j.c === X ? "rotate scrubs it and does not add the new key" : "rotate never edits a database: " + j.man;
    placeTip(j, `<b>${j.tool} · ${j.c}</b><code>${j.p}</code>${j.fld ? ` <span class="mono">${j.fld}</span>` : ""}<br>${k.name} ${k.hint}${j.why ? `<br><em>${j.why}</em>` : ""}<br>→ ${action}`);
  }
  function placeTip(j, html) {
    tip.innerHTML = html;
    const r = stage.getBoundingClientRect(), s = r.width / G.W;
    let left = j.x * s + 18, top = (j.y + 60) * s;
    tip.classList.add("show");
    const tw = tip.offsetWidth;
    if (left + tw > r.width) left = Math.max(4, j.x * s - tw - 18);
    tip.style.left = left + "px"; tip.style.top = top + "px";
  }
  function hideTip() { tip.classList.remove("show"); }

  // ── state machine ───────────────────────────────────────────────────
  function setLog(html) {
    logEl.classList.add("fade");
    setTimeout(() => { logEl.innerHTML = html; logEl.classList.remove("fade"); }, reduce ? 0 : 180);
  }
  function setMeter(text) { meter.value = text; meter.textContent = text; }
  function pressLever(name) {
    Object.entries(levers).forEach(([n, b]) => { b.classList.toggle("on", n === name); b.setAttribute("aria-pressed", n === name ? "true" : "false"); });
  }
  function lockLevers(on) { Object.values(levers).forEach(b => b.disabled = on); }

  async function selectKey(id, user) {
    if (state.rotated && id === "A") return;
    const k = keyOf(id);
    state.sel = id;
    pressLever("map");
    const busy = state.phase === "busy";
    state.phase = "busy"; lockLevers(true);
    // pull the previous key's cords
    stores.forEach(j => { if (!state.rotated) j.halo.classList.remove("on"); });
    await Promise.all(jacks.filter(j => j.cord && j.cord.k.id !== "N").map(j => { lamp(j.lamp, "off"); return pull(j, 360); }));
    KEYS.forEach(o => o.g.style.opacity = o.id === id || o.revoked ? (o.revoked ? .45 : 1) : .78);
    const mine = files.filter(j => j.k === id && !(state.rotated && id === "A"));
    setLog(mapLog(k));
    setMeter(`1 KEY · ${mine.length} ${mine.length === 1 ? "COPY" : "COPIES"}`);
    await Promise.all(mine.map((j, i) => wait(i * 110).then(() => cord(k, j, 640)).then(() => lamp(j.lamp, j.c === X ? "--hammer-hi" : "--ring"))));
    // asked by name only: no cord, just a dashed ring that says "an entry called this exists"
    stores.filter(j => j.k === id).forEach(j => j.halo.classList.add("on"));
    state.phase = busy ? "busy" : "idle"; lockLevers(false);
    levers.rotate.disabled = state.rotated || !state.checked;
    if (!busy) settle();
  }
  // a plug or jack picked while the board was busy: patch it in as soon as it is free
  function pick(id) {
    stopAuto();
    if (state.phase === "busy") { state.queued = id; return; }
    selectKey(id, true);
  }
  function settle() {
    // deferred: if a check or rotate is about to continue, it runs first and settles later
    setTimeout(() => {
      if (!state || state.phase !== "idle" || !state.queued) return;
      const q = state.queued; state.queued = null;
      selectKey(q, true);
    }, 0);
  }

  async function doCheck() {
    state.phase = "busy"; lockLevers(true); pressLever("check");
    setLog(LOG.check);
    KEYS.forEach(k => lamp(k.trunk, "--ring", "ringing"));
    await wait(2000);
    KEYS.forEach(k => {
      if (k.revoked) return;
      lamp(k.trunk, k.alive ? "--alive" : "--dead", k.alive ? null : "blink");
      k.status.textContent = k.alive ? (k.id === "R" ? "ALIVE · NO CAP" : "ALIVE") : "REJECTED";
      k.status.setAttribute("fill", k.alive ? "#1d4a2b" : "#7d1d1f");
    });
    state.checked = true;
    // the dead key's live copies: these tools fail on their next request
    if (state.sel === "A" && !state.rotated) files.filter(j => j.k === "A" && j.c !== X).forEach(j => lamp(j.lamp, "--dead", "blink"));
    setMeter(state.rotated ? "OLD KEY · 1 PLACE" : "REJECTED · 4 TOOLS");
    state.phase = "idle"; lockLevers(false); levers.rotate.disabled = state.rotated;
    settle();
  }

  async function doRotate() {
    if (state.rotated) return;
    if (state.sel !== "A") await selectKey("A");
    if (!state.checked) await doCheck();
    state.phase = "busy"; lockLevers(true); pressLever("rotate");
    setLog(LOG.rotate);
    NEW.g.style.opacity = 1;
    if (!reduce) NEW.g.animate([{ transform: "translateY(18px)", opacity: 0 }, { transform: "none", opacity: 1 }], { duration: 380, easing: "cubic-bezier(.3,1.4,.5,1)" });
    lamp(NEW.trunk, "--alive"); NEW.status.textContent = "WORKS ✓"; NEW.status.setAttribute("fill", "#1d4a2b");
    await wait(500);
    const mine = files.filter(j => j.k === "A");
    let left = mine.length;
    const order = [L, X, DB];
    for (const c of order) {
      const group = mine.filter(j => j.c === c);
      await Promise.all(group.map(async (j, i) => {
        await wait(i * 160);
        if (c === DB) { lamp(j.lamp, "--dead", "blink"); ticket(j, j.man); return; }
        await pull(j, 460);
        left--; setMeter(`OLD KEY · ${left} ${left === 1 ? "PLACE" : "PLACES"}`);
        if (c === L) { const nc = await cord(NEW, j, 620); j.cord = nc; lamp(j.lamp, "--alive"); j.tick.setAttribute("fill", css("--cord-new")); }
        else { lamp(j.lamp, "off"); busyPlug(j); }
      }));
      await wait(250);
    }
    // the Keychain entry: keyroll cannot see or edit it, so it rings for the operator
    stores.filter(j => j.k === "A").forEach(j => { j.halo.classList.add("on"); lamp(j.lamp, "--ring", "blink"); ticket(j, j.ticket); });
    const A = keyOf("A");
    A.revoked = true; A.g.style.opacity = .45; lamp(A.trunk, "--dead"); A.status.textContent = "REVOKED ✓";
    state.rotated = true;
    setMeter("OLD KEY · 9 » 1");
    state.phase = "idle"; lockLevers(false); levers.rotate.disabled = true;
    replayBtn.hidden = false;
    settle();
  }

  // autoplay once, when the board is on screen; any click takes over
  let auto = null, taken = false; // taken: a click before autoplay started still wins
  function stopAuto() { taken = true; if (auto) { auto.stop = true; } }
  async function autoplay() {
    if (taken) return;
    const run = auto = { stop: false };
    await selectKey("A");
    await wait(2600); if (run.stop) return;
    await doCheck();
    await wait(2600); if (run.stop) return;
    await doRotate();
  }

  const replayBtn = document.getElementById("replay");
  function restore() {
    // after a resize, redraw the current state without animation
    const s = state;
    state = { phase: "idle", sel: null, checked: false, rotated: false };
    KEYS.forEach(k => { k.revoked = false; });
    if (!s) return;
    fast = true;
    (async () => {
      if (s.sel) await selectKey(s.rotated ? "A" : s.sel);
      if (s.checked) await doCheck();
      if (s.rotated) await doRotate();
      if (s.rotated && s.sel !== "A") await selectKey(s.sel);
      fast = false;
    })();
  }
  function reset() {
    stopAuto(); state = null; replayBtn.hidden = true;
    NEW.g && (NEW.g.style.opacity = 0);
    draw(); pressLever(null);
    levers.rotate.disabled = true;
  }

  levers.map.addEventListener("click", () => { stopAuto(); if (state.phase !== "busy") selectKey(state.rotated ? "R" : (state.sel || "A"), true); });
  levers.check.addEventListener("click", () => { stopAuto(); if (state.phase !== "busy") doCheck(); });
  levers.rotate.addEventListener("click", () => { stopAuto(); if (state.phase !== "busy") doRotate(); });
  replayBtn.addEventListener("click", () => { reset(); taken = false; autoplay(); });

  draw();
  levers.rotate.disabled = true;
  setLog(LOG.idle); setMeter("0 KEYS");
  let lastNarrow = G.narrow;
  addEventListener("resize", () => {
    const n = stage.clientWidth < 700;
    if (n !== lastNarrow && state.phase !== "busy") { lastNarrow = n; draw(); }
  });
  const io = new IntersectionObserver(es => { if (es.some(e => e.isIntersecting)) { io.disconnect(); setTimeout(autoplay, reduce ? 0 : 500); } }, { threshold: .35 });
  io.observe(stage);

  // copy buttons
  document.querySelectorAll("[data-copy]").forEach(b => b.addEventListener("click", async () => {
    const text = document.getElementById(b.dataset.copy).textContent.replace(/^\$ /gm, "");
    try { await navigator.clipboard.writeText(text); b.textContent = "copied"; b.classList.add("ok"); }
    catch { b.textContent = "select"; }
    setTimeout(() => { b.textContent = "copy"; b.classList.remove("ok"); }, 1600);
  }));
})();
