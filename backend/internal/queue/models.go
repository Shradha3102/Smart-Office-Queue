// Package queue contains all queue / token business rules.
package queue

import "time"

// Token statuses.
const (
	StatusWaiting   = "waiting"
	StatusServing   = "serving"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
	StatusNoShow    = "no_show" // auto-cancelled after the second no-show
)

// Event types written to token_events.
const (
	EvCreated         = "created"
	EvCalled          = "called"
	EvCompleted       = "completed"
	EvNoShowRequeued  = "no_show_requeued"
	EvNoShowCancelled = "no_show_cancelled"
	EvCancelled       = "cancelled"
	EvExpired         = "expired"
	EvTransferredOut  = "transferred_out"
	EvTransferredIn   = "transferred_in"
	EvPriorityChanged = "priority_changed"
	EvPaused          = "paused"
	EvResumed         = "resumed"
)

// MaxNoShows is the number of no-shows after which a token is cancelled automatically.
const MaxNoShows = 2

// Department is a service desk with its own queue.
type Department struct {
	ID                int        `json:"id"`
	Code              string     `json:"code"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	AvgServiceMinutes int        `json:"avg_service_minutes"`
	IsPaused          bool       `json:"is_paused"`
	PauseReason       string     `json:"pause_reason"`
	PausedAt          *time.Time `json:"paused_at"`
	SortOrder         int        `json:"sort_order"`
}

// DepartmentSummary is a department plus its live queue figures.
type DepartmentSummary struct {
	Department
	WaitingCount int     `json:"waiting_count"`
	NowServing   *string `json:"now_serving"`
	// Service time actually used for ETAs (learned from recent completions, else the default).
	EffectiveServiceMinutes float64 `json:"effective_service_minutes"`
	// What a visitor generating a token right now should expect.
	EstimatedWaitMinutes int `json:"estimated_wait_minutes"`
}

// Token is a visitor's ticket.
type Token struct {
	ID              string     `json:"id"`
	TokenNumber     string     `json:"token_number"`
	DepartmentID    int        `json:"department_id"`
	DepartmentCode  string     `json:"department_code"`
	DepartmentName  string     `json:"department_name"`
	VisitorName     string     `json:"visitor_name"`
	VisitorPhone    string     `json:"visitor_phone,omitempty"`
	IsPriority      bool       `json:"is_priority"`
	PriorityReason  string     `json:"priority_reason"`
	InPriorityLane  bool       `json:"in_priority_lane"`
	Status          string     `json:"status"`
	NoShowCount     int        `json:"no_show_count"`
	TransferCount   int        `json:"transfer_count"`
	TransferredFrom string     `json:"transferred_from"`
	Source          string     `json:"source"`
	CloseReason     string     `json:"close_reason"`
	CreatedAt       time.Time  `json:"created_at"`
	EnqueuedAt      time.Time  `json:"enqueued_at"`
	CalledAt        *time.Time `json:"called_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	ClosedAt        *time.Time `json:"closed_at"`

	// Live queue figures, only present while waiting.
	Position             *int `json:"position,omitempty"`
	PeopleAhead          *int `json:"people_ahead,omitempty"`
	EstimatedWaitMinutes *int `json:"estimated_wait_minutes,omitempty"`

	lane     int
	queueSeq int64
	secret   string
}

// IsActive reports whether the token is still in a queue.
func (t *Token) IsActive() bool { return t.Status == StatusWaiting || t.Status == StatusServing }

// TokenStatus is what a visitor's tracking screen shows.
type TokenStatus struct {
	Token      Token          `json:"token"`
	Department DepartmentInfo `json:"department"`
	NowServing *string        `json:"now_serving"`
}

// DepartmentInfo is the slim department payload embedded in TokenStatus.
type DepartmentInfo struct {
	ID          int    `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	IsPaused    bool   `json:"is_paused"`
	PauseReason string `json:"pause_reason"`
}

// CreatedToken is returned once, on creation; AccessSecret lets the visitor cancel it.
type CreatedToken struct {
	TokenStatus
	AccessSecret string `json:"access_secret"`
}

// QueueView is the staff console payload for one department.
type QueueView struct {
	Department DepartmentSummary `json:"department"`
	Serving    *Token            `json:"serving"`
	Waiting    []Token           `json:"waiting"`
	Recent     []Token           `json:"recent"`
}

// NoShowResult describes what happened on a no-show.
type NoShowResult struct {
	Token  Token  `json:"token"`
	Action string `json:"action"` // "requeued" | "cancelled"
}

// DeptStats are today's figures for one department.
type DeptStats struct {
	DepartmentID          int        `json:"department_id"`
	Code                  string     `json:"code"`
	Name                  string     `json:"name"`
	IsPaused              bool       `json:"is_paused"`
	PauseReason           string     `json:"pause_reason"`
	Waiting               int        `json:"waiting"`
	PriorityWaiting       int        `json:"priority_waiting"`
	Serving               int        `json:"serving"`
	NowServing            *string    `json:"now_serving"`
	NowServingSince       *time.Time `json:"now_serving_since"`
	Issued                int        `json:"issued"`
	Called                int        `json:"called"`
	Completed             int        `json:"completed"`
	NoShows               int        `json:"no_shows"`
	AutoCancelledNoShows  int        `json:"auto_cancelled_no_shows"`
	Cancelled             int        `json:"cancelled"`
	TransferredOut        int        `json:"transferred_out"`
	AvgWaitMinutes        float64    `json:"avg_wait_minutes"`
	AvgServiceMinutes     float64    `json:"avg_service_minutes"`
	LongestWaitingMinutes float64    `json:"longest_waiting_minutes"`
}

// Dashboard aggregates today's statistics.
type Dashboard struct {
	Date        string      `json:"date"`
	Timezone    string      `json:"timezone"`
	GeneratedAt time.Time   `json:"generated_at"`
	Totals      DeptStats   `json:"totals"`
	Departments []DeptStats `json:"departments"`
}

// Event is published to realtime subscribers whenever a queue changes.
type Event struct {
	Type          string `json:"type"`
	DepartmentIDs []int  `json:"department_ids"`
	TokenID       string `json:"token_id,omitempty"`
	Action        string `json:"action"`
}

// Notifier receives queue change events (implemented by the realtime hub).
type Notifier interface {
	Publish(Event)
}

// NopNotifier discards events.
type NopNotifier struct{}

// Publish implements Notifier.
func (NopNotifier) Publish(Event) {}
