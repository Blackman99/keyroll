// Command demohome writes the fake demo home used by the README GIF and CI:
//
//	go run ./internal/testhome/demohome /tmp/demo-home
package main

import (
	"fmt"
	"os"

	"github.com/Blackman99/keyroll/internal/testhome"
)

func main() {
	if len(os.Args) != 2 && os.Getenv("DEMOHOME_KEY") == "" {
		fmt.Fprintln(os.Stderr, "usage: demohome DIR")
		os.Exit(2)
	}
	// DEMOHOME_KEY=<name> prints one fake key instead of building a home.
	if name := os.Getenv("DEMOHOME_KEY"); name != "" {
		k := testhome.Fake()
		v := map[string]string{"anthropic": k.Anthropic, "new-anthropic": k.NewAnthropic, "openai": k.OpenAI,
			"new-openai": k.NewOpenAI, "openrouter": k.OpenRouter, "google": k.Google, "deepseek": k.DeepSeek}[name]
		if v == "" {
			fmt.Fprintln(os.Stderr, "unknown key name", name)
			os.Exit(2)
		}
		fmt.Print(v)
		return
	}
	testhome.Build(os.Args[1])
}
