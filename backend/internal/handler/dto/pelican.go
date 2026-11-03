package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
)

// PelicanTask keeps historical execution metadata out of admin responses.
type PelicanTask struct {
	ID            string     `json:"id"`
	BatchID       string     `json:"batch_id"`
	AccountID     int64      `json:"account_id"`
	AccountName   string     `json:"account_name"`
	Model         string     `json:"model"`
	SourceID      string     `json:"source_id"`
	Action        string     `json:"action"`
	UpstreamModel string     `json:"upstream_model"`
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code"`
	ErrorMessage  string     `json:"error_message"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	DurationMS    *int64     `json:"duration_ms"`
	HTMLBytes     int        `json:"html_bytes"`
	ThumbnailURL  *string    `json:"thumbnail_url"`
}

type PelicanDetail struct {
	PelicanTask
	HTML string `json:"html"`
}

func PelicanTaskFromService(task benchmark.Task) PelicanTask {
	return PelicanTask{
		ID: task.ID, BatchID: task.BatchID, AccountID: task.AccountID,
		AccountName: task.AccountName, Model: task.Model, SourceID: task.SourceID,
		Action: task.Action, UpstreamModel: task.UpstreamModel, Status: task.Status,
		ErrorCode: task.ErrorCode, ErrorMessage: task.ErrorMessage, CreatedAt: task.CreatedAt,
		StartedAt: task.StartedAt, FinishedAt: task.FinishedAt, DurationMS: task.DurationMS,
		HTMLBytes: task.HTMLBytes, ThumbnailURL: task.ThumbnailURL,
	}
}

func PelicanTasksFromService(tasks []benchmark.Task) []PelicanTask {
	if tasks == nil {
		return nil
	}
	result := make([]PelicanTask, len(tasks))
	for i := range tasks {
		result[i] = PelicanTaskFromService(tasks[i])
	}
	return result
}
