package s3x

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Chế độ kết nối.
const (
	ModeS3      = "s3"      // gọi thẳng S3 API (cần endpoint public)
	ModeConsole = "console" // đi qua MinIO Console (khi S3 API không public)
)

// StorageClient: nguồn log — hoặc là S3 API trực tiếp, hoặc qua Console.
type StorageClient interface {
	ListDates(ctx context.Context) ([]string, error)
	ListRange(ctx context.Context, from, to string, fromHour, toHour int) ([]Object, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
	ListBuckets(ctx context.Context) ([]string, error)
	Test(ctx context.Context) (int, error)
	Bucket() string
}

// Connect: lần lượt thử (1) S3 API URL đã biết/tự dò, (2) chế độ qua Console.
// Trả về client + chế độ + endpoint đã dùng (để lưu lại).
func Connect(consoleURL, loginURL, user, pass, s3URL, bucket string) (StorageClient, string, string, error) {
	base := firstNonEmptyStr(consoleURL, loginURL)

	// 1) S3 API trực tiếp (URL đã lưu hoặc tự dò từ console)
	tryS3 := func(endpoint string) (StorageClient, bool) {
		if endpoint == "" {
			return nil, false
		}
		c, err := New(endpoint, user, pass, bucket)
		if err != nil {
			return nil, false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_, lerr := c.ListBuckets(ctx)
		cancel()
		if lerr != nil {
			return nil, false
		}
		return c, true
	}
	if c, ok := tryS3(s3URL); ok {
		return c, ModeS3, s3URL, nil
	}
	if base != "" {
		if ep, _, err := DiscoverAPI(base, user, pass); err == nil {
			if c, ok := tryS3(ep); ok {
				return c, ModeS3, ep, nil
			}
		}
	}

	// 2) Chế độ qua Console (S3 API không public — deployment phía nginx chỉ proxy console)
	if base != "" {
		c, err := NewConsoleClient(base, user, pass, bucket)
		if err == nil {
			return c, ModeConsole, base, nil
		}
		return nil, "", "", fmt.Errorf("không kết nối được qua S3 API lẫn Console: %v", err)
	}
	return nil, "", "", fmt.Errorf("thiếu URL chính / URL đăng nhập của MinIO Console")
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------- ConsoleClient ----------

// ConsoleClient: gọi MinIO Console API (/api/v1/...) bằng session cookie
// sau khi đăng nhập — dùng được khi S3 API không được expose ra ngoài.
type ConsoleClient struct {
	base   string
	user   string
	pass   string
	hc     *http.Client
	bucket string
}

func NewConsoleClient(consoleBase, user, pass, bucket string) (*ConsoleClient, error) {
	base, err := NormalizeConsoleBase(consoleBase)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &ConsoleClient{base: base, user: user, pass: pass, bucket: bucket, hc: &http.Client{Jar: jar}}
	if err := c.login(); err != nil {
		return nil, err
	}
	// xác minh session đọc được bucket
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.ListBuckets(ctx); err != nil {
		return nil, fmt.Errorf("đã đăng nhập console nhưng không đọc được danh sách bucket: %v", err)
	}
	return c, nil
}

func (c *ConsoleClient) login() error {
	body, _ := json.Marshal(map[string]string{"accessKey": c.user, "secretKey": c.pass})
	resp, err := c.hc.Post(c.base+"/api/v1/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("không kết nối được console %s: %v", c.base, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == 200 || resp.StatusCode == 204:
		return nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("sai tên đăng nhập hoặc mật khẩu")
	default:
		return fmt.Errorf("console trả về HTTP %d khi đăng nhập", resp.StatusCode)
	}
}

// do: gọi API console; nếu session hết hạn (401) → đăng nhập lại và thử thêm 1 lần.
func (c *ConsoleClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	doReq := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return c.hc.Do(req)
	}
	resp, err := doReq()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if lerr := c.login(); lerr != nil {
			return nil, fmt.Errorf("phiên console hết hạn và đăng nhập lại lỗi: %v", lerr)
		}
		resp, err = doReq()
	}
	return resp, nil
}

func (c *ConsoleClient) Bucket() string { return c.bucket }

func (c *ConsoleClient) ListBuckets(ctx context.Context) ([]string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/buckets", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("console trả về HTTP %d khi liệt kê bucket: %s", resp.StatusCode, snippet(data))
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		var wrapped struct {
			Buckets []map[string]any `json:"buckets"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			return nil, fmt.Errorf("danh sách bucket không đọc được (HTTP %d): %.200s", resp.StatusCode, data)
		}
		rows = wrapped.Buckets
	}
	out := make([]string, 0, len(rows))
	for _, m := range rows {
		if n := pickStr(m, "name", "Name", "bucket", "bucket_name"); n != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (c *ConsoleClient) Test(ctx context.Context) (int, error) {
	bs, err := c.ListBuckets(ctx)
	if err != nil {
		return 0, err
	}
	if c.bucket == "" {
		return len(bs), nil
	}
	dates, err := c.ListDates(ctx)
	return len(dates), err
}

// listObjectsForPrefix: liệt kê object dưới 1 prefix (recursive) qua console API.
func (c *ConsoleClient) listObjectsForPrefix(ctx context.Context, prefix string) ([]Object, error) {
	path := fmt.Sprintf("/api/v1/buckets/%s/objects?prefix=%s&recursive=true",
		url.PathEscape(c.bucket), url.QueryEscape(prefix))
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("liệt kê object trả về HTTP %d: %s", resp.StatusCode, snippet(data))
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		var wrapped struct {
			Objects []map[string]any `json:"objects"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			return nil, fmt.Errorf("danh sách object không đọc được: %.200s", data)
		}
		rows = wrapped.Objects
	}
	out := make([]Object, 0, len(rows))
	for _, m := range rows {
		name := pickStr(m, "name", "Name", "key", "Key")
		if name == "" || pickBool(m, "is_dir", "isDir", "is_prefix", "isPrefix") {
			continue
		}
		out = append(out, Object{
			Key:  name,
			Date: dateOfKey(name),
			Hour: parseHour(name),
			Size: int64(pickF64(m, "size", "Size")),
		})
	}
	return out, nil
}

func (c *ConsoleClient) ListDates(ctx context.Context) ([]string, error) {
	objs, err := c.listObjectsForPrefix(ctx, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, o := range objs {
		if o.Date != "" && !seen[o.Date] {
			seen[o.Date] = true
			out = append(out, o.Date)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (c *ConsoleClient) ListRange(ctx context.Context, from, to string, fromHour, toHour int) ([]Object, error) {
	dates, err := datesBetween(from, to)
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, date := range dates {
		objs, err := c.listObjectsForPrefix(ctx, date+"/")
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			if !strings.HasSuffix(o.Key, ".tar.gz") {
				continue
			}
			if date == from && fromHour >= 0 && (date != to || toHour < 0) {
				if o.Hour >= 0 && o.Hour < fromHour {
					continue
				}
			}
			if date == to && toHour >= 0 && (date != from || fromHour >= 0) {
				if o.Hour >= 0 && o.Hour > toHour {
					continue
				}
			}
			if date == from && date == to {
				if fromHour >= 0 && o.Hour >= 0 && o.Hour < fromHour {
					continue
				}
				if toHour >= 0 && o.Hour >= 0 && o.Hour > toHour {
					continue
				}
			}
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		if out[i].Hour != out[j].Hour {
			return out[i].Hour < out[j].Hour
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// GetObject: stream object qua console download API.
func (c *ConsoleClient) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	u := fmt.Sprintf("/api/v1/buckets/%s/objects/download?prefix=%s",
		url.PathEscape(c.bucket), url.QueryEscape(key))
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return nil, 0, fmt.Errorf("tải %s trả về HTTP %d: %s", key, resp.StatusCode, snippet(data))
	}
	return resp.Body, -1, nil
}

// ---------- tolerant JSON helpers ----------

var keyDateRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})/`)

func dateOfKey(key string) string {
	if m := keyDateRe.FindStringSubmatch(key); m != nil {
		return m[1]
	}
	return ""
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				return t
			case fmt.Stringer:
				return t.String()
			}
		}
	}
	return ""
}

func pickF64(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if f, ok := v.(float64); ok {
				return f
			}
		}
	}
	return 0
}

func pickBool(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// NormalizeConsoleBase chuẩn hóa URL console về origin:
// - thêm https:// nếu thiếu scheme
// - phần path (/login, /browser/<bucket>...) bỏ qua, chỉ giữ origin
// Kết quả ví dụ: https://minio.uongoc.vn
func NormalizeConsoleBase(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("URL trống")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("URL không hợp lệ: %v", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL thiếu host: %s", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// ConsoleLogin đăng nhập vào MinIO Console (/login) bằng username/password
// (trong MinIO Console, username/password chính là access key/secret key).
func ConsoleLogin(consoleBase, user, pass string) error {
	base, err := NormalizeConsoleBase(consoleBase)
	if err != nil {
		return err
	}
	hc := &http.Client{Timeout: 15 * time.Second}

	// MinIO Console hiện tại: POST JSON /api/v1/login
	body, _ := json.Marshal(map[string]string{"accessKey": user, "secretKey": pass})
	resp, err := hc.Post(base+"/api/v1/login", "application/json", bytes.NewReader(body))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == 200 || resp.StatusCode == 204:
			return nil
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return fmt.Errorf("sai tên đăng nhập hoặc mật khẩu")
		}
		// mã khác (404 = console cũ...) → thử tiếp dạng form bên dưới
		resp2, err2 := hc.PostForm(base+"/api/v1/login", url.Values{"accessKey": {user}, "secretKey": {pass}})
		if err2 == nil {
			io.Copy(io.Discard, resp2.Body)
			resp2.Body.Close()
			switch {
			case resp2.StatusCode == 200 || resp2.StatusCode == 204:
				return nil
			case resp2.StatusCode == 401 || resp2.StatusCode == 403:
				return fmt.Errorf("sai tên đăng nhập hoặc mật khẩu")
			}
		}
		return fmt.Errorf("trang MinIO trả về HTTP %d khi đăng nhập (kiểm tra lại URL console)", resp.StatusCode)
	}
	return fmt.Errorf("không kết nối được %s: %v", base, err)
}

// DiscoverAPI: từ URL console, thử lần lượt các vị trí phổ biến của S3 API
// (cùng domain — khi console được reverse proxy chung, hoặc cổng 9000 chuẩn của MinIO).
// Trả về endpoint chạy được + danh sách bucket.
func DiscoverAPI(consoleBase, user, pass string) (endpoint string, buckets []string, err error) {
	base, err := NormalizeConsoleBase(consoleBase)
	if err != nil {
		return "", nil, err
	}
	u, _ := url.Parse(base)
	host := u.Hostname()

	var cands []string
	add := func(s string) {
		for _, c := range cands {
			if c == s {
				return
			}
		}
		cands = append(cands, s)
	}
	if p := u.Port(); p != "" {
		add(u.Scheme + "://" + u.Host) // console có cổng riêng — thử đúng cái đã
	}
	add("https://" + host + ":9000") // cổng S3 chuẩn của MinIO
	add(u.Scheme + "://" + host)     // S3 proxy cùng domain
	add("http://" + host + ":9000")
	add("http://" + host)

	var lastErr error
	for _, cand := range cands {
		pu, perr := url.Parse(cand)
		if perr != nil {
			continue
		}
		cli, cerr := minio.New(pu.Host, &minio.Options{
			Creds:  credentials.NewStaticV4(user, pass, ""),
			Secure: pu.Scheme == "https",
		})
		if cerr != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		bs, lerr := cli.ListBuckets(ctx)
		cancel()
		if lerr != nil {
			lastErr = lerr
			continue
		}
		names := make([]string, 0, len(bs))
		for _, b := range bs {
			names = append(names, b.Name)
		}
		return cand, names, nil
	}
	if lastErr != nil {
		return "", nil, fmt.Errorf("đăng nhập console OK nhưng không gọi được S3 API từ %s: %v", base, lastErr)
	}
	return "", nil, fmt.Errorf("không tìm được S3 API nào chạy được từ %s", base)
}

// ListBuckets: liệt kê bucket của user.
func (c *Client) ListBuckets(ctx context.Context) ([]string, error) {
	bs, err := c.cli.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out, nil
}
