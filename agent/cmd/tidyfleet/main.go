// Command tidyfleet is the Tidyfleet agent: a developer-aware disk cleaner
// that can also report laptop health to a company dashboard once enrolled.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"scan", "Find regenerable developer artifacts (read-only)", cmdScan},
		{"clean", "Preview and clean eligible artifacts", cmdClean},
		{"ui", "Open the cleaner app: disk tree, preview and clean", cmdUI},
		{"log", "Show the local cleanup log", cmdLog},
		{"config", "Show or change cleaner settings (show | set <key> <value> | path)", cmdConfig},
		{"rules", "List cleanup rules", cmdRules},
		{"health", "Show this laptop's health metrics", cmdHealth},
		{"enroll", "Join an organization: enroll <server-url> <code>", cmdEnroll},
		{"report", "Send a health snapshot now (enrolled devices only)", cmdReport},
		{"shared", "Show exactly what was last shared with your organization", cmdShared},
		{"leave", "Leave your organization and stop reporting", cmdLeave},
		{"daemon", "Run in the background: reporting and weekly reminders", cmdDaemon},
		{"version", "Print the agent version", cmdVersion},
	}
}

func main() {
	reporter.UserAgent = "tidyfleet-agent/" + version
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		usage()
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, c := range commands {
		if c.name == os.Args[1] {
			if err := c.run(ctx, os.Args[2:]); err != nil {
				if !errors.Is(err, flag.ErrHelp) {
					fmt.Fprintln(os.Stderr, "tidyfleet:", err)
				}
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "tidyfleet: unknown command %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Println("Tidyfleet: a disk cleaner for developers, with optional laptop health reporting.")
	fmt.Println("\nUsage: tidyfleet <command> [flags]\n\nCommands:")
	for _, c := range commands {
		fmt.Printf("  %-9s %s\n", c.name, c.summary)
	}
	fmt.Println("\nRun `tidyfleet <command> -h` for a command's flags.")
}

func cmdVersion(ctx context.Context, args []string) error {
	fmt.Println("tidyfleet", version)
	return nil
}

func newFlags(name, argsUsage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: tidyfleet %s %s\n", name, argsUsage)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags lets flags appear after positional arguments, as users expect.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func loadRules() ([]rules.Rule, error) {
	dir, err := config.Path("rules")
	if err != nil {
		return nil, err
	}
	return rules.Load(dir)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func humanBytes(n int64) string {
	const unit = 1000 // decimal, like Finder and Explorer
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"kB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit || u == "TB" {
			if f < 10 {
				return fmt.Sprintf("%.1f %s", f, u)
			}
			return fmt.Sprintf("%.0f %s", f, u)
		}
	}
	return ""
}

// tildePath shortens paths under the home folder for display.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ask reads one line from the terminal.
func ask(prompt string) (string, error) {
	if !isTerminal(os.Stdin) {
		return "", errors.New("confirmation needed but stdin is not a terminal (use --yes)")
	}
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func confirm(prompt string) (bool, error) {
	ans, err := ask(prompt + " [y/N] ")
	if err != nil {
		return false, err
	}
	ans = strings.ToLower(ans)
	return ans == "y" || ans == "yes", nil
}
