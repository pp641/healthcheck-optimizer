package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/tidyfleet/tidyfleet/agent/internal/ui"
)

// cmdUI serves the cleaner app on 127.0.0.1 and opens it in the browser.
func cmdUI(ctx context.Context, args []string) error {
	fs := newFlags("ui", "[flags]")
	port := fs.Int("port", 0, "port on 127.0.0.1 (default: any free port)")
	noOpen := fs.Bool("no-open", false, "print the link instead of opening the browser")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	srv := &ui.Server{Version: version}
	ln, url, err := srv.Listen(*port)
	if err != nil {
		return err
	}
	fmt.Println("Tidyfleet Cleaner is running on this computer only.")
	fmt.Println("Open:", url)
	fmt.Println("Press Ctrl+C to stop.")
	if !*noOpen {
		openBrowser(url)
	}
	return srv.Serve(ctx, ln)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
