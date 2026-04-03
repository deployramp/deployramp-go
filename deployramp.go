// Package deployramp provides the Go SDK for the DeployRamp feature flag platform.
//
// Usage:
//
//	deployramp.Init(deployramp.Config{
//	    PublicToken: "your-token",
//	})
//
//	if deployramp.Flag("new-checkout") {
//	    // new checkout flow
//	}
//
//	deployramp.Close()
package deployramp

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultBaseURL = "https://flags.deployramp.com"

var (
	pkgClient    *apiClient
	pkgCache     *flagCache
	pkgTraits    map[string]string
	pkgTraitsMu  sync.RWMutex
	pkgUserID    string
	pkgUserOnce  sync.Once
	pkgInitMu    sync.Mutex
)

// getUserID returns a stable per-process user ID.
func getUserID() string {
	pkgUserOnce.Do(func() {
		pkgUserID = uuid.New().String()
	})
	return pkgUserID
}

// hashKey produces a 0-99 bucket matching the JS SDK implementation.
// JS: hash = ((hash << 5) - hash + charCodeAt(i)) | 0; return Math.abs(hash) % 100;
func hashKey(input string) int {
	h := int32(0)
	for _, ch := range input {
		h = (h << 5) - h + int32(ch)
	}
	if h < 0 {
		h = -h
	}
	return int(h) % 100
}

// matchCondition recursively evaluates a trait condition against the given traits.
func matchCondition(cond TraitCondition, traits map[string]string) bool {
	switch cond.Type {
	case "match":
		return traits[cond.TraitKey] == cond.TraitValue
	case "and":
		for _, c := range cond.Conditions {
			if !matchCondition(c, traits) {
				return false
			}
		}
		return true
	case "or":
		for _, c := range cond.Conditions {
			if matchCondition(c, traits) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// mergeTraits returns a new map that merges base traits with overrides.
func mergeTraits(base, overrides map[string]string) map[string]string {
	if overrides == nil {
		// Return a copy of base
		m := make(map[string]string, len(base))
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	m := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		m[k] = v
	}
	for k, v := range overrides {
		m[k] = v
	}
	return m
}

// getCurrentTraits returns a copy of the current traits.
func getCurrentTraits() map[string]string {
	pkgTraitsMu.RLock()
	defer pkgTraitsMu.RUnlock()
	m := make(map[string]string, len(pkgTraits))
	for k, v := range pkgTraits {
		m[k] = v
	}
	return m
}

// Init initializes the DeployRamp SDK. It fetches flags from the API
// and establishes a WebSocket connection for real-time updates.
func Init(config Config) error {
	pkgInitMu.Lock()
	defer pkgInitMu.Unlock()

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	pkgClient = newAPIClient(baseURL, config.PublicToken)
	pkgCache = newFlagCache()

	pkgTraitsMu.Lock()
	if config.Traits != nil {
		pkgTraits = make(map[string]string, len(config.Traits))
		for k, v := range config.Traits {
			pkgTraits[k] = v
		}
	} else {
		pkgTraits = make(map[string]string)
	}
	pkgTraitsMu.Unlock()

	resp, err := pkgClient.fetchFlags(getUserID(), getCurrentTraits())
	if err != nil {
		log.Printf("[deployramp] Failed to initialize: %v", err)
		return fmt.Errorf("failed to initialize: %w", err)
	}

	pkgCache.setFlags(resp.Flags)

	wsURL := buildWSURL(baseURL, config.PublicToken)
	pkgCache.connectWebSocket(wsURL)

	return nil
}

// SetTraits replaces the current global traits.
func SetTraits(traits map[string]string) {
	pkgTraitsMu.Lock()
	defer pkgTraitsMu.Unlock()
	pkgTraits = make(map[string]string, len(traits))
	for k, v := range traits {
		pkgTraits[k] = v
	}
}

// Flag evaluates a feature flag and returns whether it is enabled for the current user.
// Optional traitOverrides are merged on top of the global traits for this evaluation.
func Flag(name string, traitOverrides ...map[string]string) bool {
	if pkgCache == nil {
		return false
	}

	f := pkgCache.getFlag(name)
	if f == nil {
		queueEvaluation(name, false, traitOverrides...)
		return false
	}
	if !f.Enabled {
		queueEvaluation(name, false, traitOverrides...)
		return false
	}

	var overrides map[string]string
	if len(traitOverrides) > 0 {
		overrides = traitOverrides[0]
	}

	traits := mergeTraits(getCurrentTraits(), overrides)
	userID := getUserID()

	// Check segments for trait-based rollout
	if len(f.Segments) > 0 {
		for _, segment := range f.Segments {
			if matchCondition(segment.Condition, traits) {
				// Sticky check
				if segment.Sticky && containsString(f.StickyAssignments, segment.SegmentID) {
					queueEvaluation(name, true, traitOverrides...)
					return true
				}

				bucket := hashKey(name + ":" + userID + ":" + segment.SegmentID)
				result := bucket < segment.RolloutPercentage
				queueEvaluation(name, result, traitOverrides...)
				return result
			}
		}
	}

	// Default: use the top-level rolloutPercentage
	if f.RolloutPercentage >= 100 {
		queueEvaluation(name, true, traitOverrides...)
		return true
	}
	if f.RolloutPercentage <= 0 {
		queueEvaluation(name, false, traitOverrides...)
		return false
	}

	bucket := hashKey(name + ":" + userID)
	result := bucket < f.RolloutPercentage
	queueEvaluation(name, result, traitOverrides...)
	return result
}

// queueEvaluation records a flag evaluation event for batched reporting.
func queueEvaluation(flagName string, result bool, traitOverrides ...map[string]string) {
	if pkgCache == nil {
		return
	}
	var overrides map[string]string
	if len(traitOverrides) > 0 {
		overrides = traitOverrides[0]
	}
	traits := mergeTraits(getCurrentTraits(), overrides)
	event := EvaluationEvent{
		Type:      "evaluation",
		FlagName:  flagName,
		Result:    result,
		Traits:    traits,
		UserID:    getUserID(),
		Timestamp: time.Now().UnixMilli(),
	}
	pkgCache.queueEvaluation(event)
}

// queuePerformance records a performance measurement event for batched reporting.
func queuePerformance(flagName string, durationMs float64, branch string, traitOverrides ...map[string]string) {
	if pkgCache == nil {
		return
	}
	var overrides map[string]string
	if len(traitOverrides) > 0 {
		overrides = traitOverrides[0]
	}
	traits := mergeTraits(getCurrentTraits(), overrides)
	event := PerformanceEvent{
		Type:       "performance",
		FlagName:   flagName,
		DurationMs: durationMs,
		Branch:     branch,
		Traits:     traits,
		UserID:     getUserID(),
		Timestamp:  time.Now().UnixMilli(),
	}
	pkgCache.queuePerformance(event)
}

// Measure evaluates a feature flag, executes the appropriate branch,
// measures its execution time, and reports the metric back to DeployRamp.
func Measure(name string, enabledFn func(), disabledFn func(), traitOverrides ...map[string]string) {
	enabled := Flag(name, traitOverrides...)
	start := time.Now()
	if enabled {
		enabledFn()
	} else {
		disabledFn()
	}
	durationMs := float64(time.Since(start).Microseconds()) / 1000.0
	branch := "disabled"
	if enabled {
		branch = "enabled"
	}
	queuePerformance(name, durationMs, branch, traitOverrides...)
}

// MeasureValue evaluates a feature flag, executes the appropriate branch,
// measures its execution time, and reports the metric back to DeployRamp.
// Returns the result of the executed branch.
func MeasureValue[T any](name string, enabledFn func() T, disabledFn func() T, traitOverrides ...map[string]string) T {
	enabled := Flag(name, traitOverrides...)
	start := time.Now()
	var result T
	if enabled {
		result = enabledFn()
	} else {
		result = disabledFn()
	}
	durationMs := float64(time.Since(start).Microseconds()) / 1000.0
	branch := "disabled"
	if enabled {
		branch = "enabled"
	}
	queuePerformance(name, durationMs, branch, traitOverrides...)
	return result
}

// Report sends an error report associated with a feature flag.
func Report(err error, flagName string, traitOverrides ...map[string]string) {
	if pkgClient == nil {
		return
	}

	message := err.Error()
	stack := fmt.Sprintf("%+v", err)

	var overrides map[string]string
	if len(traitOverrides) > 0 {
		overrides = traitOverrides[0]
	}
	traits := mergeTraits(getCurrentTraits(), overrides)

	pkgClient.reportError(flagName, message, stack, getUserID(), traits)
}

// Close shuts down the SDK, flushing pending evaluations and
// disconnecting the WebSocket.
func Close() {
	pkgInitMu.Lock()
	defer pkgInitMu.Unlock()

	if pkgCache != nil {
		pkgCache.close()
		pkgCache = nil
	}
	pkgClient = nil
	pkgTraitsMu.Lock()
	pkgTraits = make(map[string]string)
	pkgTraitsMu.Unlock()
}

// containsString checks if a string slice contains a given string.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
