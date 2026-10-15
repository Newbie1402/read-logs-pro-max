// Package server: HTTP API + phục vụ UI tĩnh.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"readlogs/internal/config"
	"readlogs/internal/logs"
	"readlogs/internal/s3x"
	"readlogs/internal/store"
)

const Version = "1.0.0"

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type ScanFile struct {
	Key    string `json:"key"`
	Date   string `json:"date"`
	Hour   int    `json:"hour"`
	Size   int64  `json:"size"`
	Cached bool   `json:"cached"`
}

type App struct {
	cfg      *config.Config
	st       *store.Store
	webFS    fs.FS
	authPass string // APP_PASSWORD — rỗng = không khóa
	mu       sync.Mutex
	client   s3x.StorageClient
	mode     string // "s3" | "console"
	sessions map[string]*logs.Session
	jobs     map[string]*Job
}

func New(cfg *config.Config, st *store.Store, webFS embed.FS, authPass string) (*App, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	app := &App{
		cfg: cfg, st: st, webFS: sub, authPass: authPass,
		sessions: map[string]*logs.Session{},
		jobs:     map[string]*Job{},
	}
	app.reconnect()
	go app.gcLoop()
	return app, nil
}

func (a *App) reconnect() {
	_ = a.connect()
}

// connect: đăng nhập + chọn chế độ kết nối tốt nhất (S3 API trực tiếp, nếu không
// được thì qua MinIO Console). Trả về lỗi để UI hiển thị.
func (a *App) connect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	a.mode = ""
	if !a.cfg.Configured() {
		return fmt.Errorf("chưa cấu hình đủ thông tin MinIO")
	}
	c, mode, endpoint, err := s3x.Connect(
		a.cfg.ConsoleURL, a.cfg.LoginURL, a.cfg.Username, a.cfg.Password,
		a.cfg.MinioURL, a.cfg.Bucket,
	)
	if err != nil {
		log.Printf("[minio] kết nối lỗi: %v", err)
		return err
	}
	a.client = c
	a.mode = mode
	if mode == s3x.ModeS3 && endpoint != a.cfg.MinioURL {
		a.cfg.MinioURL = endpoint
		_ = a.cfg.Save(map[string]string{config.KeyMinioURL: endpoint})
	}
	log.Printf("[minio] sẵn sàng (chế độ %s): %s bucket=%s", mode, endpoint, a.cfg.Bucket)
	return nil
}

// clientOr400: trả về client kết nối tốt nhất hiện có (kèm chế độ).
func (a *App) clientOr400(w http.ResponseWriter) s3x.StorageClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		fail(w, 400, fmt.Errorf("chưa kết nối được MinIO — kiểm tra .env / Cài đặt"))
		return nil
	}
	return a.client
}

// discoverFromConsole: đăng nhập console + dò S3 endpoint; trả về endpoint + danh sách bucket.
// (hiện Connect() đã gộp bước này — giữ cho mục đích debug/nâng sau)
func (a *App) discoverFromConsole() (string, []string, error) {
	base := firstNonEmpty(a.cfg.ConsoleURL, a.cfg.LoginURL)
	if base == "" {
		return "", nil, fmt.Errorf("thiếu URL chính / URL đăng nhập của MinIO Console")
	}
	if a.cfg.Username == "" || a.cfg.Password == "" {
		return "", nil, fmt.Errorf("thiếu tên đăng nhập / mật khẩu")
	}
	if err := s3x.ConsoleLogin(base, a.cfg.Username, a.cfg.Password); err != nil {
		return "", nil, err
	}
	return s3x.DiscoverAPI(base, a.cfg.Username, a.cfg.Password)
}

var idSeq atomic.Int64

func (a *App) newID(prefix string) string {
	n := idSeq.Add(1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}

func (a *App) store() *store.Store {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v); err != nil {
		fail(w, 400, fmt.Errorf("body JSON không hợp lệ: %v", err))
		return false
	}
	return true
}

// ---------- API routes ----------

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("POST /api/config", a.handleSaveConfig)
	mux.HandleFunc("POST /api/settings", a.handleSaveSettings)
	mux.HandleFunc("POST /api/test", a.handleTest)
	mux.HandleFunc("GET /api/buckets", a.handleBuckets)
	mux.HandleFunc("POST /api/pick-folder", a.handlePickFolder)
	mux.HandleFunc("GET /api/dates", a.handleDates)
	mux.HandleFunc("POST /api/scan", a.handleScan)
	mux.HandleFunc("POST /api/load", a.handleLoad)
	mux.HandleFunc("GET /api/job/{id}", a.handleJob)
	mux.HandleFunc("POST /api/job/{id}/cancel", a.handleJobCancel)
	mux.HandleFunc("GET /api/session/{id}/info", a.handleSessionInfo)
	mux.HandleFunc("GET /api/session/{id}/entries", a.handleEntries)
	mux.HandleFunc("GET /api/session/{id}/search", a.handleSearch)
	mux.HandleFunc("DELETE /api/session/{id}", a.handleCloseSession)
	mux.HandleFunc("GET /api/cache", a.handleCacheStats)
	mux.HandleFunc("POST /api/cache/delete", a.handleCacheDelete)
	mux.HandleFunc("POST /api/cache/open", a.handleCacheOpen)
	mux.HandleFunc("POST /api/import-local", a.handleImportLocal)
	mux.HandleFunc("POST /api/import-upload", a.handleImportUpload)

	mux.Handle("GET /", http.FileServerFS(a.webFS))
	return a.authGate(mux)
}

// ---------- auth gate (VPS mode) ----------

const authCookie = "lvsess"

func sessionToken(pass string) string {
	h := sha256.Sum256([]byte("logviewer:" + pass))
	return hex.EncodeToString(h[:])
}

// authGate: nếu đặt APP_PASSWORD, mọi request (trừ /api/login và file tĩnh của trang
// đăng nhập) phải mang cookie phiên hợp lệ. Trang tĩnh vẫn được trả để app tự hiện
// màn hình nhập mật khẩu khi gọi API gặp 401.
func (a *App) authGate(next http.Handler) http.Handler {
	if a.authPass == "" {
		return next
	}
	want := sessionToken(a.authPass)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie(authCookie); err == nil && subtle.ConstantTimeCompare([]byte(ck.Value), []byte(want)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/healthz" {
			handleHealth(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Cần đăng nhập", "auth": "required"})
			return
		}
		// trang tĩnh: vẫn trả về để SPA tự chuyển sang màn hình khóa
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.authPass == "" {
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readBody(w, r, &body) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(a.authPass)) != 1 {
		time.Sleep(600 * time.Millisecond) // chậm tay kẻ dò mật khẩu
		fail(w, 401, fmt.Errorf("mật khẩu không đúng"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    sessionToken(a.authPass),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	clientOK := a.client != nil
	mode := a.mode
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"version":     Version,
		"configured":  a.cfg.Configured(),
		"clientReady": clientOK,
		"mode":        mode,
		"config": map[string]any{
			"consoleUrl":    a.cfg.ConsoleURL,
			"loginUrl":      a.cfg.LoginURL,
			"username":      a.cfg.Username,
			"hasPassword":   a.cfg.Password != "",
			"bucket":        a.cfg.Bucket,
			"minioUrl":      a.cfg.MinioURL,
			"cacheDir":      a.cfg.CacheDir,
			"port":          a.cfg.Port,
			"keepArchives":  a.cfg.KeepArchives,
			"autoCleanDays": a.cfg.AutoCleanDays,
			"envPath":       a.cfg.EnvPath(),
		},
	})
}

func (a *App) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !readBody(w, r, &body) {
		return
	}
	trim := func(s string) string { return strings.TrimSpace(s) }
	fields := map[string]string{}
	if v := trim(body["consoleUrl"]); v != "" {
		fields[config.KeyConsoleURL] = v
	}
	if v := trim(body["loginUrl"]); v != "" {
		fields[config.KeyLoginURL] = v
	}
	if v := trim(body["username"]); v != "" {
		fields[config.KeyUsername] = v
	}
	if v := trim(body["password"]); v != "" {
		fields[config.KeyPassword] = v
	}
	if v, ok := body["bucket"]; ok {
		fields[config.KeyBucket] = trim(v) // cho phép đặt lại/xóa bucket
	}

	// phát hiện thay đổi thông tin đăng nhập → ghi log chế độ chuyển đổi
	changedAuth :=
		(fields[config.KeyConsoleURL] != "" && fields[config.KeyConsoleURL] != a.cfg.ConsoleURL) ||
			(fields[config.KeyLoginURL] != "" && fields[config.KeyLoginURL] != a.cfg.LoginURL) ||
			(fields[config.KeyUsername] != "" && fields[config.KeyUsername] != a.cfg.Username) ||
			fields[config.KeyPassword] != ""
	if v, ok := body["minioUrl"]; ok {
		fields[config.KeyMinioURL] = trim(v) // URL S3 API điền tay (nâng cao)
	}

	if err := a.cfg.Save(fields); err != nil {
		fail(w, 500, fmt.Errorf("ghi .env lỗi: %v", err))
		return
	}
	a.cfg.Apply(fields)

	// kết nối lại theo cấu hình mới (S3 API trực tiếp hoặc qua Console)
	if err := a.connect(); err != nil {
		fail(w, 502, err)
		return
	}

	if changedAuth {
		log.Printf("[minio] thông tin đăng nhập vừa thay đổi, chế độ kết nối: %s", a.mode)
	}

	var buckets []string
	if c := a.clientOr400(w); c != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if bs, err := c.ListBuckets(ctx); err == nil {
			buckets = bs
		}
	}
	a.mu.Lock()
	clientOK := a.client != nil
	mode := a.mode
	endpoint := a.cfg.MinioURL
	if mode == s3x.ModeConsole {
		endpoint = firstNonEmpty(a.cfg.ConsoleURL, a.cfg.LoginURL)
	}
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"ok":       true,
		"mode":     mode,
		"endpoint": endpoint,
		"buckets":  buckets,
		"config": map[string]any{
			"consoleUrl":    a.cfg.ConsoleURL,
			"loginUrl":      a.cfg.LoginURL,
			"username":      a.cfg.Username,
			"hasPassword":   a.cfg.Password != "",
			"bucket":        a.cfg.Bucket,
			"minioUrl":      a.cfg.MinioURL,
			"cacheDir":      a.cfg.CacheDir,
			"port":          a.cfg.Port,
			"keepArchives":  a.cfg.KeepArchives,
			"autoCleanDays": a.cfg.AutoCleanDays,
			"envPath":       a.cfg.EnvPath(),
		},
		"configured":  a.cfg.Configured(),
		"clientReady": clientOK,
	})
}

func (a *App) handleBuckets(w http.ResponseWriter, r *http.Request) {
	c := a.clientOr400(w)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	bs, err := c.ListBuckets(ctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"buckets": bs, "current": a.cfg.Bucket})
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !readBody(w, r, &body) {
		return
	}
	fields := map[string]string{}
	if v, ok := body["cacheDir"]; ok && strings.TrimSpace(v) != "" {
		fields[config.KeyCacheDir] = strings.TrimSpace(v)
	}
	if v, ok := body["keepArchives"]; ok {
		fields[config.KeyKeepArch] = boolStr(v)
	}
	if v, ok := body["autoCleanDays"]; ok {
		fields[config.KeyCleanDays] = strings.TrimSpace(v)
	}
	if len(fields) == 0 {
		fail(w, 400, fmt.Errorf("không có thay đổi"))
		return
	}
	if err := a.cfg.Save(fields); err != nil {
		fail(w, 500, err)
		return
	}
	oldCache := a.cfg.CacheDir
	a.cfg.Apply(fields)
	if a.cfg.CacheDir != oldCache {
		st, err := store.New(a.cfg.CacheDir)
		if err != nil {
			fail(w, 500, fmt.Errorf("tạo cache dir mới lỗi: %v", err))
			return
		}
		a.mu.Lock()
		a.st = st
		a.mu.Unlock()
		a.closeAllSessions()
	}
	a.handleState(w, r)
}

func boolStr(v string) string {
	if v == "1" || strings.EqualFold(strings.TrimSpace(v), "true") {
		return "true"
	}
	return "false"
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	// kết nối lại từ đầu với cấu hình hiện tại (S3 API trực tiếp, fallback qua Console)
	if err := a.connect(); err != nil {
		fail(w, 502, err)
		return
	}
	a.mu.Lock()
	mode := a.mode
	endpoint := a.cfg.MinioURL
	if mode == s3x.ModeConsole {
		endpoint = firstNonEmpty(a.cfg.ConsoleURL, a.cfg.LoginURL)
	}
	a.mu.Unlock()

	resp := map[string]any{"ok": true, "mode": mode, "endpoint": endpoint, "bucket": a.cfg.Bucket}
	if c := a.clientOr400(w); c != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if bs, err := c.ListBuckets(ctx); err == nil {
			resp["buckets"] = bs
		}
		if a.cfg.Bucket != "" {
			if dates, err := c.ListDates(ctx); err == nil {
				resp["dates"] = len(dates)
			}
		}
	}
	writeJSON(w, 200, resp)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// requireBucket: các API liệt kê/tải cần biết bucket.
func (a *App) requireBucket(w http.ResponseWriter) bool {
	if a.cfg.Bucket == "" {
		fail(w, 400, fmt.Errorf("chưa chọn bucket chứa log — bấm 'Lấy danh sách bucket' rồi chọn, hoặc điền tay"))
		return false
	}
	return true
}

func (a *App) handleDates(w http.ResponseWriter, r *http.Request) {
	if !a.requireBucket(w) {
		return
	}
	c := a.clientOr400(w)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	dates, err := c.ListDates(ctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"dates": dates})
}

func (a *App) handleScan(w http.ResponseWriter, r *http.Request) {
	if !a.requireBucket(w) {
		return
	}
	c := a.clientOr400(w)
	if c == nil {
		return
	}
	var body struct {
		From     string `json:"from"`
		To       string `json:"to"`
		FromHour *int   `json:"fromHour"`
		ToHour   *int   `json:"toHour"`
	}
	if !readBody(w, r, &body) {
		return
	}
	fh, th := -1, -1
	if body.FromHour != nil {
		fh = *body.FromHour
	}
	if body.ToHour != nil {
		th = *body.ToHour
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	objs, err := c.ListRange(ctx, body.From, body.To, fh, th)
	if err != nil {
		fail(w, 502, err)
		return
	}
	st := a.store()
	files := make([]ScanFile, 0, len(objs))
	var total, cachedCount int64
	for _, o := range objs {
		cached := st.HasManifest(o.Key, o.Size)
		if cached {
			cachedCount++
		}
		total += o.Size
		files = append(files, ScanFile{Key: o.Key, Date: o.Date, Hour: o.Hour, Size: o.Size, Cached: cached})
	}
	writeJSON(w, 200, map[string]any{
		"files": files, "totalSize": total, "count": len(files), "cachedCount": cachedCount,
	})
}

// ---------- sessions ----------

func (a *App) closeAllSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = map[string]*logs.Session{}
}

func (a *App) putSession(s *logs.Session) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= 8 {
		// LRU: xóa session cũ nhất (chỉ bỏ index trong RAM, file cache vẫn còn)
		var oldestID string
		first := true
		for id := range a.sessions {
			if first || id < oldestID {
				oldestID, first = id, false
			}
		}
		delete(a.sessions, oldestID)
	}
	id := a.newID("s")
	s.ID = id
	a.sessions[id] = s
	return id
}

func (a *App) getSession(id string) *logs.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

// sessionFromDirs dựng session từ các thư mục đã giải nén (theo đúng thứ tự truyền vào).
func (a *App) sessionFromDirs(items []store.SessionDir) (string, error) {
	var files []*logs.FileIndex
	metas := []logs.FileMeta{}
	var totalBytes int64
	for _, it := range items {
		for _, f := range it.Manifest.Files {
			p := filepath.Join(it.Dir, f.Name)
			ix, err := logs.BuildIndex(p)
			if err != nil {
				log.Printf("[index] bỏ qua file lỗi %s: %v", p, err)
				continue
			}
			files = append(files, ix)
			totalBytes += ix.Size
			metas = append(metas, logs.FileMeta{
				Key: it.Manifest.Key, Name: f.Name, Date: it.Date,
				Entries: ix.Entries, Size: ix.Size,
			})
		}
	}
	if len(files) == 0 {
		return "", fmt.Errorf("không có file log nào trong các archive đã tải")
	}
	s := logs.NewSession("", files, metas)
	s.TotalBytes = totalBytes
	return a.putSession(s), nil
}

func (a *App) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	s := a.getSession(r.PathValue("id"))
	if s == nil {
		fail(w, 404, fmt.Errorf("session không tồn tại hoặc đã hết hạn — hãy tải lại logs"))
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":         s.ID,
		"total":      s.Total,
		"files":      s.Meta,
		"fileCount":  len(s.Meta),
		"totalBytes": s.TotalBytes,
	})
}

func (a *App) handleEntries(w http.ResponseWriter, r *http.Request) {
	s := a.getSession(r.PathValue("id"))
	if s == nil {
		fail(w, 404, fmt.Errorf("session không tồn tại hoặc đã hết hạn"))
		return
	}
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	count, _ := strconv.ParseInt(q.Get("count"), 10, 64)
	if count <= 0 || count > 1000 {
		count = 300
	}
	if from < 0 {
		from = 0
	}
	entries := s.Read(from, count)
	if entries == nil {
		entries = []logs.Entry{}
	}
	writeJSON(w, 200, map[string]any{"total": s.Total, "from": from, "entries": entries})
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	s := a.getSession(r.PathValue("id"))
	if s == nil {
		fail(w, 404, fmt.Errorf("session không tồn tại hoặc đã hết hạn"))
		return
	}
	q := r.URL.Query().Get("q")
	level := r.URL.Query().Get("level")
	max := 20000
	if m, err := strconv.Atoi(r.URL.Query().Get("max")); err == nil && m > 0 && m <= 100000 {
		max = m
	}
	matches, total, truncated, err := s.Search(q, level, max, r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"matches": matches, "total": total, "truncated": truncated})
}

func (a *App) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	delete(a.sessions, r.PathValue("id"))
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- cache ----------

func (a *App) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.store().Stats())
}

func (a *App) handleCacheDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
		Dir  string `json:"dir"`
		All  bool   `json:"all"`
	}
	if !readBody(w, r, &body) {
		return
	}
	st := a.store()
	var freed int64
	switch {
	case body.All:
		st.ClearAll()
	case body.Dir != "":
		if err := st.DeleteDir(body.Dir); err != nil {
			fail(w, 500, err)
			return
		}
	case body.Date != "":
		if !dateRe.MatchString(body.Date) && body.Date != "imported" {
			fail(w, 400, fmt.Errorf("ngày không hợp lệ"))
			return
		}
		freed = st.DeleteDate(body.Date)
	default:
		fail(w, 400, fmt.Errorf("thiếu date/dir/all"))
		return
	}
	a.closeAllSessions()
	writeJSON(w, 200, map[string]any{"ok": true, "freed": freed})
}

func (a *App) handleCacheOpen(w http.ResponseWriter, r *http.Request) {
	if err := a.store().OpenFolder(); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- import file .tar.gz local ----------

func (a *App) handleImportLocal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readBody(w, r, &body) {
		return
	}
	p := strings.TrimSpace(body.Path)
	if p == "" {
		fail(w, 400, fmt.Errorf("thiếu path"))
		return
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		fail(w, 400, fmt.Errorf("file không tồn tại: %s", p))
		return
	}
	res, err := a.store().ImportLocal(p)
	if err != nil {
		fail(w, 500, err)
		return
	}
	sid, err := a.sessionFromDirs([]store.SessionDir{{Manifest: resultManifest(res), Dir: res.Dir, Date: "imported"}})
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sessionId": sid})
}

func (a *App) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		fail(w, 400, fmt.Errorf("upload lỗi: %v", err))
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, fmt.Errorf("thiếu file upload"))
		return
	}
	defer f.Close()
	// không kiểm tra đuôi file — để Content-Type/validate nội dung gzip quyết định
	// (tránh lỗi trình duyệt chặn chọn file .tar.gz)
	st := a.store()
	tmp := filepath.Join(st.TmpDir(), fmt.Sprintf("upload-%d.tar.gz", time.Now().UnixNano()))
	out, err := os.Create(tmp)
	if err != nil {
		fail(w, 500, err)
		return
	}
	_, cpErr := io.Copy(out, f)
	out.Close()
	if cpErr != nil {
		os.Remove(tmp)
		fail(w, 500, cpErr)
		return
	}
	defer os.Remove(tmp)
	res, err := st.ImportLocal(tmp)
	if err != nil {
		fail(w, 500, err)
		return
	}
	sid, err := a.sessionFromDirs([]store.SessionDir{{Manifest: resultManifest(res), Dir: res.Dir, Date: "imported"}})
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sessionId": sid})
}

func resultManifest(res *store.ExtractResult) store.Manifest {
	return store.Manifest{Key: "imported:" + filepath.Base(res.Dir), Files: res.Files}
}

// ---------- gc ----------

func (a *App) gcLoop() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		a.mu.Lock()
		for id, j := range a.jobs {
			if j.Status != "running" && time.Since(j.startedAt) > time.Hour {
				delete(a.jobs, id)
			}
		}
		a.mu.Unlock()
	}
}
