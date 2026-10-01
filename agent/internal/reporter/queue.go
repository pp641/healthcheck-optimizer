package reporter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/health"
)

const (
	batchSize = 50
	// At one snapshot an hour this is about six weeks offline.
	maxQueued = 1000
)

type queuedSnapshot struct {
	path string
	data json.RawMessage
}

func queueDir() (string, error) {
	d, err := config.Path("queue")
	if err != nil {
		return "", err
	}
	return d, os.MkdirAll(d, 0o700)
}

// Enqueue stores a snapshot until it is sent. The oldest snapshots are
// dropped once the queue is full.
func Enqueue(snap health.Snapshot) error {
	d, err := queueDir()
	if err != nil {
		return err
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d.json", snap.TakenAt.UnixNano())
	tmp := filepath.Join(d, name+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(d, name)); err != nil {
		return err
	}
	files, err := queueFiles(d)
	if err != nil {
		return err
	}
	for len(files) > maxQueued {
		_ = os.Remove(files[0])
		files = files[1:]
	}
	return nil
}

func queueFiles(d string) ([]string, error) {
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, filepath.Join(d, e.Name()))
		}
	}
	sort.Strings(out) // zero-padded timestamps sort chronologically
	return out, nil
}

func queued(n int) ([]queuedSnapshot, error) {
	d, err := queueDir()
	if err != nil {
		return nil, err
	}
	files, err := queueFiles(d)
	if err != nil {
		return nil, err
	}
	var out []queuedSnapshot
	for _, f := range files {
		if len(out) == n {
			break
		}
		b, err := os.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue // sent by a concurrent flush
		}
		if err != nil || !json.Valid(b) {
			_ = os.Remove(f)
			continue
		}
		out = append(out, queuedSnapshot{path: f, data: b})
	}
	return out, nil
}

func removeQueued(batch []queuedSnapshot) {
	for _, q := range batch {
		_ = os.Remove(q.path)
	}
}

func queueLen() (int, error) {
	d, err := queueDir()
	if err != nil {
		return 0, err
	}
	files, err := queueFiles(d)
	return len(files), err
}

// QueueLen reports how many snapshots are waiting to be sent.
func QueueLen() int {
	n, _ := queueLen()
	return n
}

func clearQueue() error {
	d, err := config.Path("queue")
	if err != nil {
		return err
	}
	return os.RemoveAll(d)
}
