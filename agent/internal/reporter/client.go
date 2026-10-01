package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/health"
)

// ErrDeviceRemoved means the server no longer recognizes this device's token,
// e.g. an admin removed it. Local enrollment is cleared when this happens.
var ErrDeviceRemoved = errors.New("this device is no longer enrolled with the organization")

var httpClient = &http.Client{Timeout: 30 * time.Second}

// UserAgent is sent on every request; the CLI sets the version.
var UserAgent = "tidyfleet-agent"

type EnrollRequest struct {
	Code         string `json:"enroll_code"`
	Hostname     string `json:"hostname"`
	OSName       string `json:"os_name"`
	OSVersion    string `json:"os_version"`
	AgentVersion string `json:"agent_version"`
}

type enrollResponse struct {
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
	OrgName     string `json:"org_name"`
	Policy      Policy `json:"policy"`
}

// NormalizeServerURL requires https, except for local development servers.
func NormalizeServerURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLocalHost(u.Hostname())) {
		return "", fmt.Errorf("server URL must use https (http is only allowed for localhost)")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func isLocalHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Enroll registers this device with an organization using its enrollment code.
func Enroll(ctx context.Context, serverURL, code, osVersion, agentVersion string) (*Enrollment, error) {
	if e, err := LoadEnrollment(); err != nil {
		return nil, err
	} else if e != nil {
		return nil, fmt.Errorf("already enrolled with %s; run `tidyfleet leave` first", e.OrgName)
	}
	base, err := NormalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	req := EnrollRequest{
		Code:         strings.TrimSpace(code),
		Hostname:     host,
		OSName:       osName(),
		OSVersion:    osVersion,
		AgentVersion: agentVersion,
	}
	var resp enrollResponse
	if err := call(ctx, http.MethodPost, base+"/api/v1/enroll", "", req, &resp); err != nil {
		return nil, err
	}
	if resp.DeviceID == "" || resp.DeviceToken == "" {
		return nil, errors.New("server returned an incomplete enrollment")
	}
	e := &Enrollment{
		ServerURL:   base,
		DeviceID:    resp.DeviceID,
		DeviceToken: resp.DeviceToken,
		OrgName:     resp.OrgName,
		EnrolledAt:  time.Now().UTC(),
		Policy:      resp.Policy,
	}
	if err := saveEnrollment(e); err != nil {
		return nil, err
	}
	return e, nil
}

// Leave unenrolls the device. The server deletes the device and its history.
// With force, local enrollment is cleared even if the server cannot be reached.
func Leave(ctx context.Context, force bool) error {
	e, err := LoadEnrollment()
	if err != nil {
		return err
	}
	if e == nil {
		return errors.New("this device is not enrolled")
	}
	err = call(ctx, http.MethodPost, e.ServerURL+"/api/v1/leave", e.DeviceToken, nil, nil)
	if err != nil && !errors.Is(err, ErrDeviceRemoved) && !force {
		return fmt.Errorf("%w (use --force to leave without telling the server)", err)
	}
	return clearEnrollment()
}

func clearEnrollment() error {
	for _, f := range []string{enrollmentFile, lastSharedFile} {
		if err := removeState(f); err != nil {
			return err
		}
	}
	return clearQueue()
}

// SharedRecord is exactly what the organization last received from this device.
type SharedRecord struct {
	SentAt    time.Time       `json:"sent_at"`
	OrgName   string          `json:"org_name"`
	ServerURL string          `json:"server_url"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

func LastShared() (*SharedRecord, error) {
	var r SharedRecord
	if err := readState(lastSharedFile, &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

type FlushResult struct {
	Sent    int
	Dropped int
	Pending int
}

type snapshotsRequest struct {
	Snapshots []json.RawMessage `json:"snapshots"`
}

type snapshotsResponse struct {
	Accepted int     `json:"accepted"`
	Policy   *Policy `json:"policy"`
}

// Report queues a snapshot and sends everything queued.
func Report(ctx context.Context, e *Enrollment, snap health.Snapshot) (FlushResult, error) {
	if err := Enqueue(snap); err != nil {
		return FlushResult{}, err
	}
	return Flush(ctx, e)
}

// Flush sends queued snapshots oldest first. Snapshots stay queued on network
// or server errors; a batch the server rejects as invalid is dropped so it
// cannot block the queue forever.
func Flush(ctx context.Context, e *Enrollment) (FlushResult, error) {
	var res FlushResult
	for {
		batch, err := queued(batchSize)
		if err != nil {
			return res, err
		}
		if len(batch) == 0 {
			return res, nil
		}
		req := snapshotsRequest{}
		for _, q := range batch {
			req.Snapshots = append(req.Snapshots, q.data)
		}
		var resp snapshotsResponse
		err = call(ctx, http.MethodPost, e.ServerURL+"/api/v1/snapshots", e.DeviceToken, req, &resp)
		var he *httpError
		switch {
		case errors.Is(err, ErrDeviceRemoved):
			_ = clearEnrollment()
			return res, err
		case errors.As(err, &he) && he.status >= 400 && he.status < 500 && he.status != http.StatusTooManyRequests:
			removeQueued(batch)
			res.Dropped += len(batch)
			continue
		case err != nil:
			res.Pending, _ = queueLen()
			return res, err
		}
		removeQueued(batch)
		res.Sent += len(batch)
		last := batch[len(batch)-1]
		_ = writeState(lastSharedFile, SharedRecord{
			SentAt: time.Now().UTC(), OrgName: e.OrgName, ServerURL: e.ServerURL, Snapshot: last.data,
		})
		if resp.Policy != nil && *resp.Policy != e.Policy {
			e.Policy = *resp.Policy
			_ = saveEnrollment(e)
		}
	}
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("server: %s (HTTP %d)", e.msg, e.status)
	}
	return fmt.Sprintf("server returned HTTP %d", e.status)
}

func call(ctx context.Context, method, url, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized && token != "" {
		return ErrDeviceRemoved
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var eb struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &eb)
		return &httpError{status: resp.StatusCode, msg: eb.Error}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func osName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}
