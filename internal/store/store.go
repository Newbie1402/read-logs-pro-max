// Package store: quản lý thư mục cache trên đĩa — tải về stream, giải nén tar.gz
// (hỗ trợ .gz lồng nhau), manifest từng archive, thống kê và dọn rác.
package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	datePrefixRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})/`)
	hourRe       = regexp.MustCompile(`_(\d{2})-?\d{0,2}\.tar\.gz$`)
)

type ExtractedFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ExtractResult: nơi archive được giải nén + danh sách file log thu được.
type ExtractResult struct {
	Dir     string
	Files   []ExtractedFile
	Skipped bool // đã có sẵn trong cache
}

type Manifest struct {
	Key         string         `json:"key"`
	Size        int64          `json:"size"`
	ExtractedAt time.Time      `json:"extractedAt"`
	Files       []ExtractedFile `json:"files"`
}

type Store struct {
	Root string
}

func New(root string) (*Store, error) {
	s := &Store{Root: root}
	for _, d := range []string{s.extractedDir(), s.archivesDir(), s.tmpDir(), s.importedDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) extractedDir() string { return filepath.Join(s.Root, "extracted") }
func (s *Store) archivesDir() string  { return filepath.Join(s.Root, "archives") }
func (s *Store) tmpDir() string       { return filepath.Join(s.Root, "tmp") }
func (s *Store) importedDir() string  { return filepath.Join(s.Root, "imported") }

// HasManifest: archive đã được giải nén thành công trong cache chưa.
func (s *Store) HasManifest(key string, size int64) bool {
	dir, _, _ := s.ExtractDirForKey(key)
	_, ok := s.HasManifestDir(dir, key, size)
	return ok
}

// TmpDir: thư mục file tạm (uploads, archive giữ tạm).
func (s *Store) TmpDir() string { return s.tmpDir() }

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// extractDirForKey: extracted/<date>/<HH>-<base của archive>
func (s *Store) ExtractDirForKey(key string) (dir, date string, hour int) {
	date = "0000-00-00"
	if m := datePrefixRe.FindStringSubmatch(key); m != nil {
		date = m[1]
	}
	hour = -1
	base := key
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if m := hourRe.FindStringSubmatch(base); m != nil {
		hour = int(m[1][0]-'0')*10 + int(m[1][1]-'0')
		if hour < 0 || hour > 23 {
			hour = -1
		}
	}
	hh := "xx"
	if hour >= 0 {
		hh = fmt.Sprintf("%02d", hour)
	}
	return filepath.Join(s.extractedDir(), date, hh+"-"+sanitize(base)), date, hour
}

func sanitize(name string) string {
	name = strings.TrimSuffix(name, ".tar.gz")
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() > 80 {
		b.Reset()
		b.WriteString(name[len(name)-60:])
	}
	return b.String()
}

// ExtractArchive stream-decode 1 archive .tar.gz từ reader r vào cache.
// Nếu archive này đã giải nén trước đó (manifest còn nguyên) → Skipped=true.
func (s *Store) ExtractArchive(ctx context.Context, key string, size int64, r io.Reader, keepArchive bool) (*ExtractResult, error) {
	dir, _, _ := s.ExtractDirForKey(key)
	if res, ok := s.HasManifestDir(dir, key, size); ok {
		return res, nil
	}
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// keepArchive: lưu bản nén trước rồi mới giải nén từ file (có thể dùng lại sau).
	src := r
	var tmpFile string
	if keepArchive {
		archDir := filepath.Join(s.archivesDir(), dirToRel(key))
		os.MkdirAll(archDir, 0o755)
		tmpFile = filepath.Join(s.tmpDir(), randID()+".part")
		f, err := os.Create(tmpFile)
		if err != nil {
			return nil, err
		}
		_, werr := io.Copy(f, r)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(tmpFile)
			return nil, fmt.Errorf("lưu archive thất bại: %v", firstErr(werr, cerr))
		}
		fl, err := os.Open(tmpFile)
		if err != nil {
			return nil, err
		}
		defer fl.Close()
		src = fl
		defer func() {
			if mvErr := os.Rename(tmpFile, filepath.Join(archDir, sanitizeKeep(filepath.Base(key)))); mvErr != nil {
				os.Remove(tmpFile)
			}
		}()
	}
	files, err := s.untarInto(ctx, dir, src)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	m := Manifest{Key: key, Size: size, ExtractedAt: time.Now(), Files: files}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".done.json"), mb, 0o644); err != nil {
		return nil, err
	}
	return &ExtractResult{Dir: dir, Files: files}, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func sanitizeKeep(name string) string { return sanitize(name) + ".tar.gz" }

func dirToRel(key string) string {
	if m := datePrefixRe.FindStringSubmatch(key); m != nil {
		return m[1]
	}
	return "other"
}

// untarInto: giải nén vào dir. File .gz lồng nhau được gunzip tiếp; chỉ giữ file .log/.txt.
func (s *Store) untarInto(ctx context.Context, dir string, r io.Reader) ([]ExtractedFile, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("không phải gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var files []ExtractedFile
	used := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, fmt.Errorf("lỗi tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == "" || base == "." || base == ".." {
			continue
		}
		isGz := strings.HasSuffix(strings.ToLower(base), ".gz")
		outName := base
		if isGz {
			outName = strings.TrimSuffix(base, base[len(base)-3:])
		}
		low := strings.ToLower(outName)
		// chỉ lấy file log/text; bỏ phần còn lại để cache sạch
		if !strings.Contains(low, ".log") && !strings.Contains(low, ".txt") {
			continue
		}
		// chống trùng tên trong cùng 1 archive
		used[outName]++
		if n := used[outName]; n > 1 {
			ext := filepath.Ext(outName)
			outName = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(outName, ext), n, ext)
		}
		dstPath := filepath.Join(dir, outName)
		df, err := os.Create(dstPath)
		if err != nil {
			return files, err
		}
		var werr error
		if isGz {
			gzn, gerr := gzip.NewReader(tr)
			if gerr != nil {
				// file .gz rỗng/hỏng → tạo file rỗng rồi tiếp tục
				werr = df.Close()
			} else {
				_, werr = io.Copy(df, gzn)
				gzn.Close()
				werr = firstErr(werr, df.Close())
			}
		} else {
			_, werr = io.Copy(df, tr)
			werr = firstErr(werr, df.Close())
		}
		if werr != nil {
			return files, werr
		}
		if st, serr := os.Stat(dstPath); serr == nil {
			files = append(files, ExtractedFile{Name: outName, Size: st.Size()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func (s *Store) HasManifestDir(dir, key string, size int64) (*ExtractResult, bool) {
	mb, err := os.ReadFile(filepath.Join(dir, ".done.json"))
	if err != nil {
		return nil, false
	}
	var m Manifest
	if json.Unmarshal(mb, &m) != nil || m.Key != key {
		return nil, false
	}
	for _, f := range m.Files {
		if st, err := os.Stat(filepath.Join(dir, f.Name)); err != nil || st.Size() != f.Size {
			return nil, false
		}
	}
	return &ExtractResult{Dir: dir, Files: m.Files, Skipped: true}, true
}

// ImportLocal: giải nén 1 file .tar.gz trên máy vào imported/<stamp>/.
func (s *Store) ImportLocal(path string) (*ExtractResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.importedDir(), time.Now().Format("20060102-150405")+"-"+randID())
	os.MkdirAll(dir, 0o755)
	files, err := s.untarInto(context.Background(), dir, f)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	m := Manifest{Key: "imported:" + filepath.Base(path), Size: st.Size(), ExtractedAt: time.Now(), Files: files}
	mb, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(filepath.Join(dir, ".done.json"), mb, 0o644)
	return &ExtractResult{Dir: dir, Files: files}, nil
}

// ListManifests đọc toàn bộ manifest trong extracted/ và imported/ (để dựng session).
func (s *Store) ListManifests(keys map[string]bool) ([]SessionDir, error) {
	var out []SessionDir
	root := s.extractedDir()
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, de := range entries {
			if !de.IsDir() {
				continue
			}
			date := de.Name()
			hours, err := os.ReadDir(filepath.Join(root, date))
			if err != nil {
				continue
			}
			for _, he := range hours {
				if !he.IsDir() {
					continue
				}
				dir := filepath.Join(root, date, he.Name())
				if m, ok := ReadManifest(dir); ok && (keys == nil || keys[m.Key]) {
					out = append(out, SessionDir{Manifest: m, Dir: dir, Date: date})
				}
			}
		}
	}
	imp, err := os.ReadDir(s.importedDir())
	if err == nil {
		for _, de := range imp {
			if !de.IsDir() {
				continue
			}
			dir := filepath.Join(s.importedDir(), de.Name())
			if m, ok := ReadManifest(dir); ok && (keys == nil || keys[m.Key]) {
				out = append(out, SessionDir{Manifest: m, Dir: dir, Date: "imported"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

type SessionDir struct {
	Manifest Manifest
	Dir      string
	Date     string
}

func ReadManifest(dir string) (Manifest, bool) {
	mb, err := os.ReadFile(filepath.Join(dir, ".done.json"))
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if json.Unmarshal(mb, &m) != nil || len(m.Files) == 0 {
		return Manifest{}, false
	}
	return m, true
}

// ---- thống kê & dọn rác ----

type DateStat struct {
	Date  string          `json:"date"`
	Bytes int64           `json:"bytes"`
	Files int             `json:"files"`
	Dirs  []DirStat       `json:"dirs"`
}

type DirStat struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

type Stats struct {
	Root       string     `json:"root"`
	TotalBytes int64      `json:"totalBytes"`
	TotalFiles int        `json:"totalFiles"`
	Extracted  []DateStat `json:"extracted"`
	ArchBytes  int64      `json:"archBytes"`
	Imported   []DateStat `json:"imported"`
}

func (s *Store) Stats() *Stats {
	st := &Stats{Root: s.Root}
	st.Extracted = s.statTree(s.extractedDir())
	st.Imported = s.statTree(s.importedDir())
	for _, d := range st.Extracted {
		st.TotalBytes += d.Bytes
		st.TotalFiles += d.Files
	}
	for _, d := range st.Imported {
		st.TotalBytes += d.Bytes
		st.TotalFiles += d.Files
	}
	filepath.WalkDir(s.archivesDir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				st.ArchBytes += info.Size()
				st.TotalBytes += info.Size()
			}
		}
		return nil
	})
	return st
}

func (s *Store) statTree(root string) []DateStat {
	var out []DateStat
	days, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, de := range days {
		if !de.IsDir() {
			continue
		}
		ds := DateStat{Date: de.Name()}
		sub, err := os.ReadDir(filepath.Join(root, de.Name()))
		if err != nil {
			continue
		}
		for _, se := range sub {
			p := filepath.Join(root, de.Name(), se.Name())
			if se.IsDir() {
				inner := DirStat{Name: se.Name()}
				filepath.WalkDir(p, func(fp string, fd os.DirEntry, err error) error {
					if err == nil && !fd.IsDir() && filepath.Base(fp) != ".done.json" {
						if info, e := fd.Info(); e == nil {
							inner.Bytes += info.Size()
							inner.Files++
						}
					}
					return nil
				})
				ds.Bytes += inner.Bytes
				ds.Files += inner.Files
				ds.Dirs = append(ds.Dirs, inner)
			} else if se.Name() != ".done.json" {
				if info, e := se.Info(); e == nil {
					ds.Bytes += info.Size()
					ds.Files++
					ds.Dirs = append(ds.Dirs, DirStat{Name: se.Name(), Bytes: info.Size(), Files: 1})
				}
			}
		}
		out = append(out, ds)
	}
	return out
}

// CleanOld xóa dữ liệu cache cũ hơn days ngày (days<=0: bỏ qua). Trả về số thư mục đã xóa.
func (s *Store) CleanOld(days int) int {
	if days <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	removed := 0
	clean := func(root string, parseDir func(name string) (time.Time, bool)) {
		days_, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, de := range days_ {
			if !de.IsDir() {
				continue
			}
			t, ok := parseDir(de.Name())
			if !ok {
				t, ok = dirTime(filepath.Join(root, de.Name()))
			}
			if ok && t.Before(cutoff) {
				if os.RemoveAll(filepath.Join(root, de.Name())) == nil {
					removed++
				}
			}
		}
	}
	clean(s.extractedDir(), func(name string) (time.Time, bool) {
		t, err := time.Parse("2006-01-02", name)
		return t.Add(24 * time.Hour), err == nil // hết ngày mới tính là cũ
	})
	clean(s.importedDir(), func(string) (time.Time, bool) { return time.Time{}, false })
	clean(s.archivesDir(), func(string) (time.Time, bool) { return time.Time{}, false })
	// dọn file .part tồn đọng
	tmps, _ := os.ReadDir(s.tmpDir())
	for _, t := range tmps {
		if strings.HasSuffix(t.Name(), ".part") || strings.HasSuffix(t.Name(), ".tar.gz") {
			if info, err := t.Info(); err == nil && info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(s.tmpDir(), t.Name()))
			}
		}
	}
	return removed
}

func dirTime(path string) (time.Time, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return st.ModTime(), true
}

// DeleteDate xóa 1 ngày (extracted + archives). Trả về số byte đã giải phóng.
func (s *Store) DeleteDate(date string) int64 {
	var freed int64
	measure := func(p string) int64 {
		var n int64
		filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, e := d.Info(); e == nil {
					n += info.Size()
				}
			}
			return nil
		})
		return n
	}
	for _, p := range []string{filepath.Join(s.extractedDir(), date), filepath.Join(s.archivesDir(), date)} {
		if _, err := os.Stat(p); err == nil {
			freed += measure(p)
			os.RemoveAll(p)
		}
	}
	// imported: xóa theo tên thư mục chứa (UI gửi tên thư mục)
	return freed
}

// DeleteDir xóa 1 thư mục con bất kỳ trong cache (đã validate trước đó).
func (s *Store) DeleteDir(rel string) error {
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return fmt.Errorf("đường dẫn không hợp lệ")
	}
	return os.RemoveAll(filepath.Join(s.Root, clean))
}

func (s *Store) ClearAll() error {
	for _, d := range []string{s.extractedDir(), s.archivesDir(), s.importedDir()} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		os.MkdirAll(d, 0o755)
	}
	return nil
}

// OpenFolder mở thư mục cache bằng Finder/Explorer.
func (s *Store) OpenFolder() error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", s.Root).Start()
	case "windows":
		return exec.Command("explorer", s.Root).Start()
	default:
		return exec.Command("xdg-open", s.Root).Start()
	}
}

// RemoveTmpParts dọn file tạm còn tồn khi khởi động.
func (s *Store) RemoveTmpParts() {
	entries, _ := os.ReadDir(s.tmpDir())
	for _, e := range entries {
		os.Remove(filepath.Join(s.tmpDir(), e.Name()))
	}
}
