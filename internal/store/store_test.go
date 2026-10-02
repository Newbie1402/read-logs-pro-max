package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tạo 1 archive .tar.gz mẫu: accountant-service.log + rotated .gz + thư mục con
func makeSampleArchive(t *testing.T, dir string) string {
	t.Helper()
	archivePath := filepath.Join(dir, "accountant_logs_2026-09-30_07-00.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	add := func(name, content string) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	add("./accountant-service.log", "2026-09-30T07:00:00.000Z  INFO 1 --- [app] [t-1] c.e.A : plain log line\n")
	// file .gz lồng nhau
	var inner strings.Builder
	inner.WriteString("2026-09-30T06:59:00.000Z  INFO 1 --- [app] [t-2] c.e.A : from rotated gz\n")
	inner.WriteString("2026-09-30T06:59:01.000Z ERROR 1 --- [app] [t-3] c.e.B : error line\n")
	var zbuf strings.Builder
	zw := gzip.NewWriter(&zbuf)
	zw.Write([]byte(inner.String()))
	zw.Close()
	add("./accountant-service.log.2026-09-30.0.gz", zbuf.String())
	// file không phải log → bị bỏ
	add("./random-data.bin", "\x00\x01\x02")
	// thư mục con chứa log
	add("./failed_batches/failed-tickets.log", "2026-09-30T07:00:05.000Z  WARN 1 --- [app] [t-4] c.e.C : failed batch\n")

	tw.Close()
	gz.Close()
	f.Close()
	return archivePath
}

func TestExtractArchive(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	key := "2026-09-30/accountant_logs_2026-09-30_07-00.tar.gz"
	data, err := os.ReadFile(makeSampleArchive(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.ExtractArchive(context.Background(), key, int64(len(data)), strings.NewReader(string(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatal("lần đầu không được Skip")
	}
	// mong đợi 3 file log (bỏ .bin)
	if len(res.Files) != 3 {
		t.Fatalf("files = %v", res.Files)
	}
	names := map[string]bool{}
	for _, f := range res.Files {
		names[f.Name] = true
	}
	for _, want := range []string{"accountant-service.log", "accountant-service.log.2026-09-30.0", "failed-tickets.log"} {
		if !names[want] {
			t.Errorf("thiếu file %s, có: %v", want, res.Files)
		}
	}
	// .gz lồng phải được giải nén
	content, err := os.ReadFile(filepath.Join(res.Dir, "accountant-service.log.2026-09-30.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "from rotated gz") {
		t.Errorf("gunzip lồng sai: %q", content)
	}
	// thư mục đích theo date/hour
	if !strings.Contains(res.Dir, filepath.Join("2026-09-30", "07-")) {
		t.Errorf("dir sai: %s", res.Dir)
	}

	// lần 2 → Skipped (cache hit)
	res2, err := s.ExtractArchive(context.Background(), key, int64(len(data)), strings.NewReader(string(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Skipped {
		t.Error("lần 2 phải Skipped=true")
	}

	// HasManifest
	if !s.HasManifest(key, int64(len(data))) {
		t.Error("HasManifest phải true")
	}
}

func TestCleanOld(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	// giả lập ngày cũ
	oldDate := "2020-01-01"
	os.MkdirAll(filepath.Join(s.extractedDir(), oldDate, "05-x"), 0o755)
	os.WriteFile(filepath.Join(s.extractedDir(), oldDate, "05-x", "a.log"), []byte("x"), 0o644)
	newDate := "2100-01-01"
	os.MkdirAll(filepath.Join(s.extractedDir(), newDate, "05-x"), 0o755)

	n := s.CleanOld(14)
	if n < 1 {
		t.Fatalf("CleanOld xóa %d, muốn >=1", n)
	}
	if _, err := os.Stat(filepath.Join(s.extractedDir(), oldDate)); !os.IsNotExist(err) {
		t.Error("thư mục cũ phải bị xóa")
	}
	if _, err := os.Stat(filepath.Join(s.extractedDir(), newDate)); err != nil {
		t.Error("thư mục mới phải còn")
	}
}

func TestStatsAndDeleteDate(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	key := "2026-09-30/accountant_logs_2026-09-30_07-00.tar.gz"
	data, _ := os.ReadFile(makeSampleArchive(t, t.TempDir()))
	s.ExtractArchive(context.Background(), key, int64(len(data)), strings.NewReader(string(data)), true) // keepArchive=true

	st := s.Stats()
	if st.TotalBytes == 0 {
		t.Error("Stats phải > 0")
	}
	if st.ArchBytes == 0 {
		t.Error("archive giữ lại phải được tính")
	}
	freed := s.DeleteDate("2026-09-30")
	if freed == 0 {
		t.Error("DeleteDate phải giải phóng > 0")
	}
	st2 := s.Stats()
	if st2.TotalBytes != 0 {
		t.Errorf("sau xóa phải = 0, còn %d", st2.TotalBytes)
	}
}

func TestImportLocal(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	archivePath := makeSampleArchive(t, t.TempDir())
	res, err := s.ImportLocal(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 {
		t.Fatalf("imported files = %v", res.Files)
	}
	if !strings.Contains(res.Dir, "imported") {
		t.Errorf("dir phải nằm trong imported/: %s", res.Dir)
	}
}
