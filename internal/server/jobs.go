package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"readlogs/internal/s3x"
	"readlogs/internal/store"
)

// Job: tiến trình tải + giải nén 1 loạt archive.
type Job struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"` // running|done|error|canceled
	Phase      string   `json:"phase"`  // download|index
	Total      int      `json:"total"`
	Done       int      `json:"done"`
	BytesTotal int64    `json:"bytesTotal"`
	BytesDone  int64    `json:"bytesDone"`
	Current    string   `json:"current"`
	Errors     []string `json:"errors"`
	SessionID  string   `json:"sessionId,omitempty"`
	cancel     context.CancelFunc
	startedAt  time.Time
}

type loadRequest struct {
	Files []ScanFile `json:"files"`
}

func (a *App) handleLoad(w http.ResponseWriter, r *http.Request) {
	c := a.clientOr400(w)
	if c == nil {
		return
	}
	var body loadRequest
	if !readBody(w, r, &body) {
		return
	}
	if len(body.Files) == 0 {
		fail(w, 400, fmt.Errorf("không có file nào được chọn"))
		return
	}
	if len(body.Files) > 3000 {
		fail(w, 400, fmt.Errorf("quá nhiều file trong 1 lần tải (%d > 3000) — thu hẹp khoảng ngày", len(body.Files)))
		return
	}
	jobID := a.newID("job")
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID: jobID, Status: "running", Phase: "download",
		Total: len(body.Files), cancel: cancel, startedAt: time.Now(),
	}
	for _, f := range body.Files {
		job.BytesTotal += f.Size
	}
	a.mu.Lock()
	a.jobs[jobID] = job
	a.mu.Unlock()
	go a.runJob(ctx, job, c, body.Files)
	writeJSON(w, 200, map[string]string{"jobId": jobID})
}

func (a *App) handleJob(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	j := a.jobs[r.PathValue("id")]
	a.mu.Unlock()
	if j == nil {
		fail(w, 404, fmt.Errorf("job không tồn tại"))
		return
	}
	writeJSON(w, 200, j)
}

func (a *App) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	j := a.jobs[r.PathValue("id")]
	a.mu.Unlock()
	if j == nil {
		fail(w, 404, fmt.Errorf("job không tồn tại"))
		return
	}
	if j.Status == "running" {
		j.Status = "canceled"
		j.cancel()
	}
	writeJSON(w, 200, j)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (a *App) runJob(ctx context.Context, job *Job, c s3x.StorageClient, files []ScanFile) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[job] panic: %v\n%s", rec, debug.Stack())
			job.Status = "error"
			job.Errors = append(job.Errors, fmt.Sprintf("lỗi hệ thống: %v", rec))
		}
	}()
	a.mu.Lock()
	st := a.st
	keep := a.cfg.KeepArchives
	a.mu.Unlock()

	var dirs []store.SessionDir
	for _, f := range files {
		if ctx.Err() != nil {
			job.Status = "canceled"
			return
		}
		if job.Status == "canceled" {
			return
		}
		job.Current = f.Key
		if st.HasManifest(f.Key, f.Size) {
			dir, _, _ := st.ExtractDirForKey(f.Key)
			if m, ok := store.ReadManifest(dir); ok {
				dirs = append(dirs, store.SessionDir{Manifest: m, Dir: dir, Date: f.Date})
			}
			job.Done++
			job.BytesDone += f.Size
			continue
		}
		rc, _, err := c.GetObject(ctx, f.Key)
		if err != nil {
			job.Errors = append(job.Errors, fmt.Sprintf("%s: %v", f.Key, err))
			job.Done++
			continue
		}
		counting := &countingReader{r: rc}
		// size dùng nhất quán cho manifest là size từ listing (console mode không trả size khi stream)
		res, err := st.ExtractArchive(ctx, f.Key, f.Size, counting, keep)
		rc.Close()
		if err != nil {
			job.Errors = append(job.Errors, fmt.Sprintf("%s: %v", f.Key, err))
		} else {
			dirs = append(dirs, store.SessionDir{Manifest: resultManifest(res), Dir: res.Dir, Date: f.Date})
		}
		job.BytesDone += counting.n
		job.Done++
	}
	if job.Status == "canceled" || ctx.Err() != nil {
		job.Status = "canceled"
		return
	}
	if len(dirs) == 0 {
		job.Status = "error"
		if len(job.Errors) == 0 {
			job.Errors = append(job.Errors, "không tải được file nào")
		}
		return
	}
	job.Phase = "index"
	job.Current = ""
	sid, err := a.sessionFromDirs(dirs)
	if err != nil {
		job.Status = "error"
		job.Errors = append(job.Errors, err.Error())
		return
	}
	job.SessionID = sid
	job.Status = "done"
}

// helper dùng chung cho import: chuyển ExtractResult → Manifest JSON-ready.
func manifestToJSON(m store.Manifest) string {
	b, _ := json.Marshal(m)
	return string(b)
}
