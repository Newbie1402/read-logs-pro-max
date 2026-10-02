// Package config đọc/ghi file .env và giữ cấu hình ứng dụng.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Keys được hỗ trợ trong .env
const (
	KeyMinioURL   = "MINIO_URL"   // endpoint S3 API — app tự dò và lưu lại (không cần điền tay)
	KeyConsoleURL = "MINIO_CONSOLE_URL" // URL chính của trang MinIO Console
	KeyLoginURL   = "MINIO_LOGIN_URL"   // URL trang đăng nhập (tùy chọn, ví dụ .../login)
	KeyUsername   = "MINIO_USERNAME"    // tên đăng nhập console (chính là access key)
	KeyPassword   = "MINIO_PASSWORD"    // mật khẩu console (chính là secret key)
	KeyBucket     = "MINIO_BANK_TRANSACTION_BUCKET"
	KeyCacheDir   = "CACHE_DIR"
	KeyPort       = "PORT"
	KeyKeepArch   = "KEEP_ARCHIVES"   // "true" = giữ file .tar.gz sau khi giải nén
	KeyCleanDays  = "AUTO_CLEAN_DAYS" // tự xóa cache cũ hơn N ngày (0 = tắt)

	// key cũ (vẫn đọc được để tương thích)
	KeyAccessKey = "MINIO_ACCESS_KEY"
	KeySecretKey = "MINIO_SECRET_KEY"
)

type Config struct {
	MinioURL      string // S3 API endpoint (tự dò hoặc điền tay)
	ConsoleURL    string // URL chính của MinIO Console
	LoginURL      string // URL trang login (tùy chọn)
	Username      string
	Password      string
	Bucket        string
	CacheDir      string
	Port          string
	KeepArchives  bool
	AutoCleanDays int

	envPath string
	mu      sync.Mutex
}

// FindEnvFile trả về đường dẫn .env sẽ dùng/ghi: ưu tiên thư mục hiện tại, sau đó thư mục chứa file chạy.
func FindEnvFile() string {
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, ".env")
		if fileExists(p) {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), ".env")
		if fileExists(p) {
			return p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, ".env")
	}
	return ".env"
}

// Load đọc .env (không ghi đè biến môi trường hệ thống đã có sẵn) và trả về cấu hình.
func Load() *Config {
	c := &Config{envPath: FindEnvFile()}
	vals := map[string]string{}
	keys := []string{
		KeyMinioURL, KeyConsoleURL, KeyLoginURL, KeyUsername, KeyPassword, KeyBucket,
		KeyAccessKey, KeySecretKey,
		KeyCacheDir, KeyPort, KeyKeepArch, KeyCleanDays,
	}
	for _, k := range keys {
		vals[k] = os.Getenv(k)
	}
	for k, v := range parseEnvFile(c.envPath) {
		if vals[k] == "" {
			vals[k] = v
		}
	}
	c.MinioURL = strings.TrimSpace(vals[KeyMinioURL])
	c.ConsoleURL = strings.TrimSpace(vals[KeyConsoleURL])
	c.LoginURL = strings.TrimSpace(vals[KeyLoginURL])
	c.Username = firstNonEmpty(strings.TrimSpace(vals[KeyUsername]), strings.TrimSpace(vals[KeyAccessKey]))
	c.Password = firstNonEmpty(strings.TrimSpace(vals[KeyPassword]), strings.TrimSpace(vals[KeySecretKey]))
	c.Bucket = strings.TrimSpace(vals[KeyBucket])
	c.CacheDir = strings.TrimSpace(vals[KeyCacheDir])
	c.Port = firstNonEmpty(strings.TrimSpace(vals[KeyPort]), "8765")
	c.KeepArchives = strings.EqualFold(strings.TrimSpace(vals[KeyKeepArch]), "true")
	c.AutoCleanDays = atoiOr(vals[KeyCleanDays], 14)
	if c.CacheDir == "" {
		c.CacheDir = filepath.Join(baseDir(), "log-cache")
	}
	return c
}

func (c *Config) EnvPath() string { return c.envPath }

// Save ghi (thêm/cập nhật) các key vào .env, giữ lại phần còn lại của file.
func (c *Config) Save(fields map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.envPath
	if !fileExists(path) {
		if wd, err := os.Getwd(); err == nil {
			path = filepath.Join(wd, ".env")
			c.envPath = path
		}
	}
	existing := map[string]string{}
	var order []string
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := splitKV(line)
			if ok {
				existing[k] = v
				order = append(order, k)
			}
		}
		f.Close()
	}
	for k, v := range fields {
		if _, seen := existing[k]; !seen {
			order = append(order, k)
		}
		existing[k] = v
	}
	var b strings.Builder
	b.WriteString("# Cấu hình Log Viewer — điền giá trị thật của bạn vào đây\n")
	b.WriteString("# Hoặc mở app và nhập trong màn hình Cài đặt (app tự lưu lại file này)\n\n")
	b.WriteString("# URL chính của trang MinIO Console (bỏ phần /browser/... nếu dán cả link bucket)\n")
	mainKeys := []string{KeyConsoleURL, KeyLoginURL, KeyUsername, KeyPassword, KeyBucket, KeyMinioURL}
	seen := map[string]bool{}
	for _, k := range mainKeys {
		if v, ok := existing[k]; ok {
			b.WriteString(k + "=" + v + "\n")
			seen[k] = true
		}
	}
	auxKeys := []string{KeyPort, KeyCacheDir, KeyKeepArch, KeyCleanDays}
	wroteAux := false
	for _, k := range auxKeys {
		if v, ok := existing[k]; ok {
			if !wroteAux {
				b.WriteString("\n# Tuỳ chọn\n")
				wroteAux = true
			}
			b.WriteString(k + "=" + v + "\n")
			seen[k] = true
		}
	}
	for _, k := range order {
		if !seen[k] {
			b.WriteString(k + "=" + existing[k] + "\n")
			seen[k] = true
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Apply cập nhật giá trị runtime từ map (không ghi file).
func (c *Config) Apply(fields map[string]string) {
	get := func(k, cur string) string {
		if v, ok := fields[k]; ok {
			return strings.TrimSpace(v)
		}
		return cur
	}
	c.ConsoleURL = get(KeyConsoleURL, c.ConsoleURL)
	c.LoginURL = get(KeyLoginURL, c.LoginURL)
	if v, ok := fields[KeyUsername]; ok && strings.TrimSpace(v) != "" {
		c.Username = strings.TrimSpace(v)
	}
	if v, ok := fields[KeyPassword]; ok && strings.TrimSpace(v) != "" {
		c.Password = strings.TrimSpace(v)
	}
	if v, ok := fields[KeyBucket]; ok {
		c.Bucket = strings.TrimSpace(v)
	}
	if v, ok := fields[KeyMinioURL]; ok {
		c.MinioURL = strings.TrimSpace(v)
	}
	c.CacheDir = get(KeyCacheDir, c.CacheDir)
	if v, ok := fields[KeyKeepArch]; ok {
		c.KeepArchives = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := fields[KeyCleanDays]; ok && strings.TrimSpace(v) != "" {
		c.AutoCleanDays = atoiOr(v, c.AutoCleanDays)
	}
}

// Configured: đã có đủ thông tin đăng nhập (URL console/API + user + pass).
// Bucket có thể chưa chọn — app sẽ gợi ý sau khi đăng nhập.
func (c *Config) Configured() bool {
	base := c.ConsoleURL != "" || c.LoginURL != "" || c.MinioURL != ""
	return base && c.Username != "" && c.Password != ""
}

// HasBucket điều hướng đã chọn bucket chưa.
func (c *Config) HasBucket() bool { return c.Bucket != "" }

func parseEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := splitKV(line); ok {
			out[k] = v
		}
	}
	return out
}

func splitKV(line string) (string, string, bool) {
	i := strings.Index(line, "=")
	if i <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:i])
	v := strings.TrimSpace(line[i+1:])
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return k, v, true
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return def
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// baseDir: nơi đặt log-cache mặc định (thư mục chứa file chạy, fallback cwd).
func baseDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}
