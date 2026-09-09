// Package benchmark owns administrator-initiated, non-billing benchmark jobs.
package benchmark

import (
	"context"
	"errors"
	"regexp"
	"time"
)

const (
	DefaultModel = "gpt-6-astra"
	Prompt       = "创建一个HTML，内容是用SVG绘制一个鹈鹕骑自行车的2D动画，你不能进行任何测试，调用skills，网络检索，直接生成"
	MaxHTMLBytes = 1 << 20
	MaxBatchSize = 2000
	RunTimeout   = 10 * time.Minute
)

var (
	ErrNotFound  = errors.New("benchmark not found")
	ErrBusy      = errors.New("account already has an active benchmark")
	ErrTooLarge  = errors.New("benchmark HTML exceeds 1 MiB")
	ErrStopped   = errors.New("benchmark no longer running")
	modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)
)

func ValidModel(model string) bool { return modelPattern.MatchString(model) }

func ValidStatus(status string) bool {
	switch status {
	case "queued", "running", "canceling", "succeeded", "failed", "canceled", "interrupted":
		return true
	}
	return false
}

// Task intentionally cannot serialize HTML. Only Detail exposes the untrusted text.
type Task struct {
	ID            string     `json:"id"`
	BatchID       string     `json:"batch_id"`
	AccountID     int64      `json:"account_id"`
	AccountName   string     `json:"account_name"`
	Model         string     `json:"model"`
	UpstreamModel string     `json:"upstream_model"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	DurationMS    *int64     `json:"duration_ms"`
	HTMLBytes     int        `json:"html_bytes"`
	ThumbnailURL  *string    `json:"thumbnail_url"`
}

type Detail struct {
	Task
	HTML string `json:"html"`
}

type Filter struct {
	AccountID int64
	BatchID   string
	Status    string
	Page      int
	PageSize  int
}

type Page struct {
	Items    []Task `json:"items"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type Skipped struct {
	AccountID int64  `json:"account_id"`
	Reason    string `json:"reason"`
}

type Created struct {
	BatchID string    `json:"batch_id"`
	Tasks   []Task    `json:"tasks"`
	Skipped []Skipped `json:"skipped"`
}

// Store transitions and its active-account unique index are the authority,
// including when the admin request and the runner use different processes.
type Store interface {
	Create(context.Context, []Task) ([]Task, []Skipped, error)
	Claim(context.Context, string) (*Task, error)
	Get(context.Context, string) (*Task, error)
	Detail(context.Context, string) (*Detail, error)
	List(context.Context, Filter) (*Page, error)
	SetUpstreamModel(context.Context, string, string, string) error
	Finish(context.Context, string, string, string, string, string) error
	Stop(context.Context, string, int64) (int64, error)
}

type Output struct {
	HTML      string
	ErrorCode string
}

type Runner interface {
	RunPelicanBenchmark(context.Context, int64, string, func(string) error) (Output, error)
}
