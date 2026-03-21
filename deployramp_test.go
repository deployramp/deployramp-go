package deployramp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var testFlags = []FlagData{
	{Name: "new-checkout", Enabled: true, RolloutPercentage: 100, Value: nil},
	{Name: "dark-mode", Enabled: false, RolloutPercentage: 50, Value: nil},
	{Name: "beta-feature", Enabled: true, RolloutPercentage: 50, Value: func() *string { s := "variant-a"; return &s }()},
	{Name: "zero-rollout", Enabled: true, RolloutPercentage: 0, Value: nil},
}

func setupMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sdk/flags":
			if r.Method != "POST" {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			resp := fetchFlagsResponse{Flags: testFlags}
			json.NewEncoder(w).Encode(resp)
		case "/api/sdk/report":
			if r.Method != "POST" {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func resetSDK() {
	Close()
	// Reset the user ID so tests are isolated
	pkgUserOnce = *new(syncOnceResettable)
	pkgUserID = ""
}

// syncOnceResettable is a trick — we reassign pkgUserOnce to a zero-value sync.Once
// We can't actually reset sync.Once, so we use a fresh one.
type syncOnceResettable = sync.Once

func TestInitAndFlag(t *testing.T) {
	server := setupMockServer(t)
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 100% rollout, enabled → true
	if !Flag("new-checkout") {
		t.Error("expected new-checkout to be true")
	}

	// disabled → false
	if Flag("dark-mode") {
		t.Error("expected dark-mode to be false")
	}

	// unknown flag → false
	if Flag("nonexistent") {
		t.Error("expected nonexistent flag to be false")
	}

	// 0% rollout → false
	if Flag("zero-rollout") {
		t.Error("expected zero-rollout to be false")
	}
}

func TestFlagBeforeInit(t *testing.T) {
	defer resetSDK()

	// Before Init, Flag should return false
	if Flag("new-checkout") {
		t.Error("expected false before Init")
	}
}

func TestCloseResetsState(t *testing.T) {
	server := setupMockServer(t)
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !Flag("new-checkout") {
		t.Error("expected new-checkout to be true before close")
	}

	Close()

	if Flag("new-checkout") {
		t.Error("expected new-checkout to be false after close")
	}
}

func TestInitWithTraits(t *testing.T) {
	var receivedBody fetchFlagsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sdk/flags" {
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fetchFlagsResponse{Flags: testFlags})
		}
	}))
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "my-token",
		BaseURL:     server.URL,
		Traits:      map[string]string{"env": "nonprod"},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if receivedBody.Traits["env"] != "nonprod" {
		t.Errorf("expected trait env=nonprod, got %v", receivedBody.Traits)
	}
}

func TestInitSendsAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sdk/flags" {
			receivedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fetchFlagsResponse{Flags: testFlags})
		}
	}))
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "my-token",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if receivedAuth != "Bearer my-token" {
		t.Errorf("expected 'Bearer my-token', got '%s'", receivedAuth)
	}
}

func TestReport(t *testing.T) {
	var receivedReport reportRequest
	reportCh := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sdk/flags":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fetchFlagsResponse{Flags: testFlags})
		case "/api/sdk/report":
			json.NewDecoder(r.Body).Decode(&receivedReport)
			w.WriteHeader(http.StatusOK)
			reportCh <- struct{}{}
		}
	}))
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Report(errors.New("Something broke"), "new-checkout")

	select {
	case <-reportCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for report")
	}

	if receivedReport.FlagName != "new-checkout" {
		t.Errorf("expected flagName 'new-checkout', got '%s'", receivedReport.FlagName)
	}
	if receivedReport.Message != "Something broke" {
		t.Errorf("expected message 'Something broke', got '%s'", receivedReport.Message)
	}
}

func TestHashKey(t *testing.T) {
	// Verify deterministic output
	h1 := hashKey("test-flag:user-123")
	h2 := hashKey("test-flag:user-123")
	if h1 != h2 {
		t.Errorf("hashKey not deterministic: %d vs %d", h1, h2)
	}

	// Verify range
	for _, input := range []string{"a", "hello", "flag:user:seg", ""} {
		h := hashKey(input)
		if h < 0 || h >= 100 {
			t.Errorf("hashKey(%q) = %d, out of range [0, 100)", input, h)
		}
	}
}

func TestMatchCondition(t *testing.T) {
	traits := map[string]string{"env": "prod", "plan": "enterprise"}

	// match
	if !matchCondition(TraitCondition{Type: "match", TraitKey: "env", TraitValue: "prod"}, traits) {
		t.Error("expected match for env=prod")
	}
	if matchCondition(TraitCondition{Type: "match", TraitKey: "env", TraitValue: "staging"}, traits) {
		t.Error("expected no match for env=staging")
	}

	// and
	andCond := TraitCondition{
		Type: "and",
		Conditions: []TraitCondition{
			{Type: "match", TraitKey: "env", TraitValue: "prod"},
			{Type: "match", TraitKey: "plan", TraitValue: "enterprise"},
		},
	}
	if !matchCondition(andCond, traits) {
		t.Error("expected and-condition to match")
	}

	// or
	orCond := TraitCondition{
		Type: "or",
		Conditions: []TraitCondition{
			{Type: "match", TraitKey: "env", TraitValue: "staging"},
			{Type: "match", TraitKey: "plan", TraitValue: "enterprise"},
		},
	}
	if !matchCondition(orCond, traits) {
		t.Error("expected or-condition to match")
	}

	// unknown type
	if matchCondition(TraitCondition{Type: "unknown"}, traits) {
		t.Error("expected unknown type to not match")
	}
}

func TestSegmentEvaluation(t *testing.T) {
	segmentFlags := []FlagData{
		{
			Name:              "segment-flag",
			Enabled:           true,
			RolloutPercentage: 0,
			Segments: []FlagSegment{
				{
					SegmentID:         "seg-1",
					Condition:         TraitCondition{Type: "match", TraitKey: "env", TraitValue: "prod"},
					RolloutPercentage: 100,
					Sticky:            false,
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sdk/flags" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fetchFlagsResponse{Flags: segmentFlags})
		}
	}))
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
		Traits:      map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Segment matches with 100% rollout → true
	if !Flag("segment-flag") {
		t.Error("expected segment-flag to be true when segment matches")
	}

	// With non-matching traits, falls through to default (0%) → false
	if Flag("segment-flag", map[string]string{"env": "staging"}) {
		t.Error("expected segment-flag to be false when segment doesn't match")
	}
}

func TestStickyAssignment(t *testing.T) {
	stickyFlags := []FlagData{
		{
			Name:              "sticky-flag",
			Enabled:           true,
			RolloutPercentage: 0,
			Segments: []FlagSegment{
				{
					SegmentID:         "seg-sticky",
					Condition:         TraitCondition{Type: "match", TraitKey: "env", TraitValue: "prod"},
					RolloutPercentage: 0, // Would normally fail
					Sticky:            true,
				},
			},
			StickyAssignments: []string{"seg-sticky"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sdk/flags" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fetchFlagsResponse{Flags: stickyFlags})
		}
	}))
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
		Traits:      map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Even with 0% rollout, sticky assignment should return true
	if !Flag("sticky-flag") {
		t.Error("expected sticky-flag to be true due to sticky assignment")
	}
}

func TestSetTraits(t *testing.T) {
	server := setupMockServer(t)
	defer server.Close()
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	SetTraits(map[string]string{"plan": "pro"})

	traits := getCurrentTraits()
	if traits["plan"] != "pro" {
		t.Errorf("expected plan=pro, got %v", traits)
	}
}

func TestBuildWSURL(t *testing.T) {
	tests := []struct {
		baseURL  string
		token    string
		expected string
	}{
		{
			"https://flags.deployramp.com",
			"tok123",
			"wss://flags.deployramp.com/ws?token=tok123",
		},
		{
			"http://localhost:3000",
			"my token",
			"ws://localhost:3000/ws?token=my+token",
		},
	}

	for _, tt := range tests {
		result := buildWSURL(tt.baseURL, tt.token)
		if result != tt.expected {
			t.Errorf("buildWSURL(%q, %q) = %q, want %q", tt.baseURL, tt.token, result, tt.expected)
		}
	}
}

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for 'b' in [a,b,c]")
	}
	if containsString([]string{"a", "b", "c"}, "d") {
		t.Error("expected false for 'd' in [a,b,c]")
	}
	if containsString(nil, "a") {
		t.Error("expected false for nil slice")
	}
}

func TestMergeTraits(t *testing.T) {
	base := map[string]string{"a": "1", "b": "2"}
	overrides := map[string]string{"b": "3", "c": "4"}

	merged := mergeTraits(base, overrides)
	if merged["a"] != "1" || merged["b"] != "3" || merged["c"] != "4" {
		t.Errorf("unexpected merge result: %v", merged)
	}

	// nil overrides
	merged2 := mergeTraits(base, nil)
	if merged2["a"] != "1" || merged2["b"] != "2" {
		t.Errorf("unexpected merge result with nil overrides: %v", merged2)
	}

	// Ensure original is not modified
	base["a"] = "changed"
	if merged2["a"] != "1" {
		t.Error("mergeTraits should return a copy")
	}
}

func TestInitFailsOnBadServer(t *testing.T) {
	defer resetSDK()

	err := Init(Config{
		PublicToken: "test-token",
		BaseURL:     "http://127.0.0.1:1", // port 1 should be unreachable
	})
	if err == nil {
		t.Error("expected Init to return error for unreachable server")
	}
	if !strings.Contains(err.Error(), "failed to initialize") {
		t.Errorf("unexpected error message: %v", err)
	}
}
