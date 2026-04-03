package deployramp

// Config holds the configuration for the DeployRamp SDK.
type Config struct {
	PublicToken string
	BaseURL     string // defaults to "https://flags.deployramp.com"
	Traits      map[string]string
}

// TraitCondition represents a condition for trait-based targeting.
type TraitCondition struct {
	Type       string           `json:"type"` // "match", "and", "or"
	TraitKey   string           `json:"traitKey,omitempty"`
	TraitValue string           `json:"traitValue,omitempty"`
	Conditions []TraitCondition `json:"conditions,omitempty"`
}

// FlagSegment represents a segment within a feature flag for targeted rollout.
type FlagSegment struct {
	SegmentID         string         `json:"segmentId"`
	Condition         TraitCondition `json:"condition"`
	RolloutPercentage int            `json:"rolloutPercentage"`
	Sticky            bool           `json:"sticky"`
}

// FlagData represents a feature flag as returned by the API.
type FlagData struct {
	Name              string        `json:"name"`
	Enabled           bool          `json:"enabled"`
	RolloutPercentage int           `json:"rolloutPercentage"`
	Value             *string       `json:"value"`
	Segments          []FlagSegment `json:"segments,omitempty"`
	StickyAssignments []string      `json:"stickyAssignments,omitempty"`
}

// EvaluationEvent records a single flag evaluation for batched reporting.
type EvaluationEvent struct {
	Type      string            `json:"type"` // always "evaluation"
	FlagName  string            `json:"flagName"`
	Result    bool              `json:"result"`
	Traits    map[string]string `json:"traits"`
	UserID    string            `json:"userId"`
	Timestamp int64             `json:"timestamp"`
}

// PerformanceEvent records a single performance measurement for batched reporting.
type PerformanceEvent struct {
	Type       string            `json:"type"` // always "performance"
	FlagName   string            `json:"flagName"`
	DurationMs float64           `json:"durationMs"`
	Branch     string            `json:"branch"` // "enabled" or "disabled"
	Traits     map[string]string `json:"traits"`
	UserID     string            `json:"userId"`
	Timestamp  int64             `json:"timestamp"`
}

type wsMessage struct {
	Type              string             `json:"type"`
	Flags             []FlagData         `json:"flags,omitempty"`
	Evaluations       []EvaluationEvent  `json:"evaluations,omitempty"`
	PerformanceEvents []PerformanceEvent `json:"performanceEvents,omitempty"`
}

type fetchFlagsRequest struct {
	UserID string            `json:"userId,omitempty"`
	Traits map[string]string `json:"traits,omitempty"`
}

type fetchFlagsResponse struct {
	Flags []FlagData `json:"flags"`
}

type reportRequest struct {
	FlagName string            `json:"flagName"`
	Message  string            `json:"message"`
	Stack    string            `json:"stack,omitempty"`
	UserID   string            `json:"userId,omitempty"`
	Traits   map[string]string `json:"traits,omitempty"`
}
