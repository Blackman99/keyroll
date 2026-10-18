// Package mcp is a read-only, offline MCP stdio server: an agent can ask which
// tools share a key (by fingerprint) but can never see a value, check a key
// over the network, or rotate anything.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Blackman99/keyroll/internal/scan"
)

// Scanner runs a scan (injectable for tests).
type Scanner func() (*scan.Report, error)

const instructions = "keyroll maps where AI provider API keys are copied on this machine. " +
	"It returns fingerprints and redacted hints only, never key values, and makes no network calls. " +
	"To check liveness or rotate, ask the user to run `keyroll check` / `keyroll rotate <fingerprint>` in their own terminal."

// Run serves JSON-RPC 2.0 over newline-delimited stdio.
func Run(in io.Reader, out io.Writer, version string, sc Scanner) error {
	br := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			handle(line, out, version, sc)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func handle(line []byte, out io.Writer, version string, sc Scanner) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		write(out, nil, nil, &rpcErr{-32700, "parse error"})
		return
	}
	notification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		write(out, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "keyroll", "version": version},
			"instructions":    instructions,
		}, nil)
	case "ping":
		write(out, req.ID, map[string]any{}, nil)
	case "tools/list":
		write(out, req.ID, map[string]any{"tools": tools()}, nil)
	case "tools/call":
		text, isErr := call(req.Params.Name, req.Params.Arguments, sc)
		write(out, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}, nil)
	default:
		if !notification {
			write(out, req.ID, nil, &rpcErr{-32601, "method not found: " + req.Method})
		}
	}
}

func tools() []map[string]any {
	ro := map[string]any{"readOnlyHint": true, "openWorldHint": false, "destructiveHint": false}
	return []map[string]any{
		{
			"name":        "key_map",
			"description": "List every AI provider API key found on this machine (agent harness configs, app state, shell files, .env files, backups, history) grouped by fingerprint, with every place each copy sits. Values are never returned, only provider, redacted hint (prefix + last 4) and fingerprint. Offline.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"provider": map[string]any{"type": "string", "description": "Only this provider id (anthropic, openai, openrouter, google, groq, xai, deepseek, mistral, ...)"},
			}},
			"annotations": ro,
		},
		{
			"name":        "key_copies",
			"description": "For one key fingerprint (or a unique prefix, or its last 4 characters), list every file that holds a copy, whether rotation would rewrite it (live), scrub it (leftover) or needs a manual step (database). Offline, read-only.",
			"inputSchema": map[string]any{"type": "object", "required": []string{"fingerprint"}, "properties": map[string]any{
				"fingerprint": map[string]any{"type": "string", "description": "8-hex fingerprint from key_map, a unique prefix, or the last 4 characters of the key hint"},
			}},
			"annotations": ro,
		},
	}
}

func call(name string, args map[string]any, sc Scanner) (string, bool) {
	r, err := sc()
	if err != nil {
		return "scan failed: " + err.Error(), true
	}
	switch name {
	case "key_map":
		if p, _ := args["provider"].(string); p != "" {
			var keep []*scan.Key
			for _, k := range r.Keys {
				if k.Provider == p {
					keep = append(keep, k)
				}
			}
			r.Keys = keep
		}
		b, _ := json.MarshalIndent(r, "", " ")
		return string(b), false
	case "key_copies":
		q, _ := args["fingerprint"].(string)
		ks := r.Find(q)
		switch len(ks) {
		case 0:
			return fmt.Sprintf("no key matches %q; call key_map for fingerprints", q), true
		case 1:
			b, _ := json.MarshalIndent(ks[0], "", " ")
			return string(b), false
		}
		return fmt.Sprintf("%q matches %d keys; use more characters of the fingerprint", q, len(ks)), true
	}
	return "unknown tool: " + name, true
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func write(out io.Writer, id json.RawMessage, result any, e *rpcErr) {
	msg := map[string]any{"jsonrpc": "2.0"}
	if len(id) > 0 {
		msg["id"] = id
	} else {
		msg["id"] = nil
	}
	if e != nil {
		msg["error"] = e
	} else {
		msg["result"] = result
	}
	b, _ := json.Marshal(msg)
	fmt.Fprintf(out, "%s\n", b)
}
