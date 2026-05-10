package nest

import (
	"errors"
	"net/http"
	"time"
)

// http endpoint paths registered in router/router_server_nest.go.
const (
	CapturePath = "/nest/capture"
	RestorePath = "/nest/restore"
)

// CaptureRequest is the body wings receives at POST /api/servers/{uuid}/nest/capture.
// the panel issues this to start an eviction. wings answers 202 immediately and
// runs the streaming capture in a goroutine.
type CaptureRequest struct {
	// PresignedUrl is a single PUT url for the s3 object key the panel chose.
	// wings streams the tar.zst body to this url.
	PresignedUrl string `json:"presigned_url" binding:"required,url"`

	// CallbackUrl is the panel endpoint wings POSTs the completion notice to.
	// shape: https://panel.internal/api/remote/servers/{uuid}/nest/captured.
	CallbackUrl string `json:"callback_url" binding:"required,url"`
}

// RestoreRequest is the body wings receives at POST /api/servers/{uuid}/nest/restore.
type RestoreRequest struct {
	// PresignedUrl is a single GET url for the archive object.
	PresignedUrl string `json:"presigned_url" binding:"required,url"`

	// ExpectedSha256 is the sha256 the panel recorded on the original capture.
	// wings verifies after the streamed extract.
	ExpectedSha256 string `json:"expected_sha256" binding:"required,hexadecimal,len=64"`

	// CallbackUrl is the panel endpoint wings POSTs the completion notice to.
	// shape: https://panel.internal/api/remote/servers/{uuid}/nest/restored.
	CallbackUrl string `json:"callback_url" binding:"required,url"`
}

// CallbackPayload is the json body wings POSTs to the panel callback urls. one
// shape covers both capture and restore, the panel discriminates on success.
type CallbackPayload struct {
	Success      bool      `json:"success"`
	ErrorMessage string    `json:"error_message,omitempty"`
	Size         int64     `json:"size,omitempty"`
	Sha256       string    `json:"sha256,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

const (
	// CallbackTimeout caps the wings → panel callback POST. the callback fires
	// once per capture or restore so the budget can be generous.
	CallbackTimeout = 30 * time.Second

	// CaptureUploadTimeout caps how long wings spends pushing to s3. worst
	// case 10 GB at gigabit lan is roughly 50s, this leaves 10x headroom.
	CaptureUploadTimeout = 10 * time.Minute

	// RestoreDownloadTimeout caps how long wings spends pulling from s3.
	RestoreDownloadTimeout = 10 * time.Minute
)

// errors returned by capture and restore goroutines, surfaced to the panel
// via CallbackPayload.ErrorMessage strings.
var (
	ErrPresignedUploadFailed   = errors.New("presigned PUT to s3 failed")
	ErrPresignedDownloadFailed = errors.New("presigned GET from s3 failed")
	ErrShaMismatch             = errors.New("sha256 mismatch on restore")
	ErrVolumeAlreadyExists     = errors.New("volume directory already exists at restore destination")
)

// httpClient is shared by the capture upload, the restore download, and the
// panel callback POST. per-request contexts narrow the timeout to the right
// envelope for each leg.
var httpClient = &http.Client{
	Timeout: 0,
}
