package reporter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/health"
)

type fakeServer struct {
	mu       sync.Mutex
	received []json.RawMessage
	down     bool
	status   int
	left     bool
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var req EnrollRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Code != "TF-GOOD" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"unknown enrollment code"}`))
			return
		}
		json.NewEncoder(w).Encode(enrollResponse{
			DeviceID: "dev-1", DeviceToken: "tok", OrgName: "Acme",
			Policy: Policy{ReportIntervalMinutes: 60, AllowAI: true},
		})
	})
	mux.HandleFunc("POST /api/v1/snapshots", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		var req snapshotsRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.received = append(f.received, req.Snapshots...)
		json.NewEncoder(w).Encode(snapshotsResponse{
			Accepted: len(req.Snapshots),
			Policy:   &Policy{ReportIntervalMinutes: 30, AllowAI: false},
		})
	})
	mux.HandleFunc("POST /api/v1/leave", func(w http.ResponseWriter, r *http.Request) {
		f.left = true
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func snap(t time.Time, disk float64) health.Snapshot {
	return health.Snapshot{TakenAt: t, Metrics: health.Metrics{DiskUsedPct: disk}}
}

func TestEnrollQueueFlushLeave(t *testing.T) {
	t.Setenv("TIDYFLEET_HOME", t.TempDir())
	fs := &fakeServer{}
	srv := httptest.NewServer(fs.handler(t))
	defer srv.Close()
	ctx := context.Background()

	if _, err := Enroll(ctx, srv.URL, "TF-BAD", "15.0", "test"); err == nil {
		t.Fatal("expected bad code to fail")
	}
	e, err := Enroll(ctx, srv.URL, "TF-GOOD", "15.0", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Enroll(ctx, srv.URL, "TF-GOOD", "15.0", "test"); err == nil {
		t.Fatal("expected second enroll to fail")
	}

	// Offline: snapshots stay queued.
	fs.down = true
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := Report(ctx, e, snap(base.Add(time.Duration(i)*time.Hour), float64(50+i))); err == nil {
			t.Fatal("expected error while server is down")
		}
	}
	if n := QueueLen(); n != 3 {
		t.Fatalf("queued = %d, want 3", n)
	}

	// Back online: everything is sent oldest first, and the policy updates.
	fs.down = false
	res, err := Flush(ctx, e)
	if err != nil || res.Sent != 3 || QueueLen() != 0 {
		t.Fatalf("flush = %+v, %v; queue %d", res, err, QueueLen())
	}
	var first health.Snapshot
	json.Unmarshal(fs.received[0], &first)
	if first.Metrics.DiskUsedPct != 50 {
		t.Fatalf("first sent disk = %v, want oldest (50)", first.Metrics.DiskUsedPct)
	}
	if got, _ := LoadEnrollment(); got.Policy.ReportIntervalMinutes != 30 || got.Policy.AllowAI {
		t.Fatalf("policy not updated: %+v", got.Policy)
	}
	shared, _ := LastShared()
	var last health.Snapshot
	json.Unmarshal(shared.Snapshot, &last)
	if last.Metrics.DiskUsedPct != 52 {
		t.Fatalf("last shared disk = %v, want 52", last.Metrics.DiskUsedPct)
	}

	// A batch rejected as invalid is dropped rather than retried forever.
	fs.status = http.StatusBadRequest
	res, _ = Report(ctx, e, snap(base.Add(5*time.Hour), 1))
	if res.Dropped != 1 || QueueLen() != 0 {
		t.Fatalf("invalid batch not dropped: %+v", res)
	}
	fs.status = 0

	if err := Leave(ctx, false); err != nil || !fs.left {
		t.Fatalf("leave: %v", err)
	}
	if e, _ := LoadEnrollment(); e != nil {
		t.Fatal("enrollment should be cleared")
	}
	if s, _ := LastShared(); s != nil {
		t.Fatal("last shared should be cleared")
	}
}

func TestRemovedDeviceClearsEnrollment(t *testing.T) {
	t.Setenv("TIDYFLEET_HOME", t.TempDir())
	srv := httptest.NewServer((&fakeServer{}).handler(t))
	defer srv.Close()
	e, err := Enroll(context.Background(), srv.URL, "TF-GOOD", "15.0", "test")
	if err != nil {
		t.Fatal(err)
	}
	e.DeviceToken = "revoked"
	if _, err := Report(context.Background(), e, snap(time.Now(), 1)); err != ErrDeviceRemoved {
		t.Fatalf("err = %v, want ErrDeviceRemoved", err)
	}
	if got, _ := LoadEnrollment(); got != nil {
		t.Fatal("enrollment should be cleared")
	}
}

func TestQueueCap(t *testing.T) {
	t.Setenv("TIDYFLEET_HOME", t.TempDir())
	base := time.Now()
	for i := 0; i < maxQueued+5; i++ {
		if err := Enqueue(snap(base.Add(time.Duration(i)*time.Second), 1)); err != nil {
			t.Fatal(err)
		}
	}
	if n := QueueLen(); n != maxQueued {
		t.Fatalf("queue = %d, want %d", n, maxQueued)
	}
}

func TestNormalizeServerURL(t *testing.T) {
	cases := map[string]string{
		"fleet.example.com":          "https://fleet.example.com",
		"https://fleet.example.com/": "https://fleet.example.com",
		"http://localhost:8080":      "http://localhost:8080",
		"http://127.0.0.1:8080":      "http://127.0.0.1:8080",
	}
	for in, want := range cases {
		if got, err := NormalizeServerURL(in); err != nil || got != want {
			t.Errorf("%s -> %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeServerURL("http://fleet.example.com"); err == nil {
		t.Error("plain http to a remote host should be rejected")
	}
}
