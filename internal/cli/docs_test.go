package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestEveryCommandIsDocumented keeps the CLI page of the docs site in step with the commands.
func TestEveryCommandIsDocumented(t *testing.T) {
	page, err := os.ReadFile("../../docs/site/reference/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, c := range cmd.Commands() {
			if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
				continue
			}
			if c.HasSubCommands() {
				walk(c)
				continue
			}
			path := strings.TrimPrefix(c.CommandPath(), "rowbird ")
			if !strings.Contains(string(page), path) {
				t.Errorf("%q is not documented in docs/site/reference/cli.md", path)
			}
			c.Flags().VisitAll(func(f *pflag.Flag) {
				if f.Name != "help" && !strings.Contains(string(page), "--"+f.Name) {
					t.Errorf("%s --%s is not documented", path, f.Name)
				}
			})
		}
	}
	walk(NewRootCommand(Env{}))
}
