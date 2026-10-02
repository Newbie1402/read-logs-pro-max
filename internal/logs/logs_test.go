package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleHeader = "2026-09-15T14:49:24.108Z  INFO 1 --- [evc-accounting] [AsyncJobBankMigrationThread-5206] c.e.e.s.b.p.SMS.SmsLegacyApiClient       : >>> [SUCCESS] Đồng bộ SMS sang Legacy thành công. MACK=ACB150920260223, Payload: {\"MACK\":\"ACB150920260223\",\"NGANHANG\":\"ACB\"}"

func TestParseHeader(t *testing.T) {
	ts, level, app, thread, logger, msg, ok := ParseHeader(sampleHeader)
	if !ok {
		t.Fatal("ParseHeader fails với dòng chuẩn")
	}
	if ts != "2026-09-15T14:49:24.108Z" {
		t.Errorf("ts = %q", ts)
	}
	if level != "INFO" {
		t.Errorf("level = %q", level)
	}
	if app != "evc-accounting" {
		t.Errorf("app = %q", app)
	}
	if thread != "AsyncJobBankMigrationThread-5206" {
		t.Errorf("thread = %q", thread)
	}
	if logger != "c.e.e.s.b.p.SMS.SmsLegacyApiClient" {
		t.Errorf("logger = %q", logger)
	}
	if !strings.HasPrefix(msg, ">>> [SUCCESS]") {
		t.Errorf("msg = %q", msg)
	}
}

func TestParseHeaderErrorLevel(t *testing.T) {
	line := "2026-09-15T14:49:24.108Z ERROR 1 --- [evc-accounting] [tomcat-handler-405] a.b.C : boom"
	_, level, _, _, _, _, ok := ParseHeader(line)
	if !ok || level != "ERROR" {
		t.Fatalf("level=%q ok=%v", level, ok)
	}
}

func TestIsStart(t *testing.T) {
	if !IsStart(sampleHeader) {
		t.Error("header phải là start")
	}
	if IsStart("\tat com.example.Foo.bar(Foo.java:12)") {
		t.Error("stack trace không phải start")
	}
}

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 5000 entries ~ 250KB → nhiều chunk 64KB
func buildContent(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("2026-09-15T14:49:24.")
		b.WriteString(pad3(i % 1000))
		b.WriteString("Z  INFO 1 --- [app] [thread-")
		b.WriteString(pad3(i % 100))
		b.WriteString("] c.e.Logger : mesage so ")
		b.WriteString(pad3(i))
		b.WriteString(" {\"k\":")
		b.WriteString(pad3(i))
		b.WriteString("}\n")
		if i%10 == 0 {
			b.WriteString("\tat com.example.Foo.bar(Foo.java:12)\n")
			b.WriteString("\tat com.example.Baz.qux(Baz.java:3)\n")
		}
	}
	return b.String()
}

func pad3(n int) string { return fmt.Sprintf("%03d", n) }

func TestBuildIndexAndRead(t *testing.T) {
	p := writeTestFile(t, buildContent(5000))
	ix, err := BuildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Entries != 5000 {
		t.Fatalf("Entries = %d, muốn 5000", ix.Entries)
	}
	if len(ix.Chunks) < 2 {
		t.Fatalf("Chunks = %d, muốn >= 2", len(ix.Chunks))
	}

	// đọc đầu
	entries, err := ReadEntries(ix, 0, 3)
	if err != nil || len(entries) != 3 {
		t.Fatalf("Read(0,3) = %d entries, err=%v", len(entries), err)
	}
	if entries[0].Idx != 0 || !strings.HasPrefix(entries[0].Msg, "mesage so 000 {\"k\":000}") {
		t.Errorf("entry0 msg = %q", entries[0].Msg)
	}

	// đọc giữa (vượt chunk)
	mid, err := ReadEntries(ix, 2500, 5)
	if err != nil || len(mid) != 5 {
		t.Fatalf("Read(2500,5) = %d entries, err=%v", len(mid), err)
	}
	for i, e := range mid {
		if e.Idx != int64(2500+i) {
			t.Errorf("mid[%d].Idx = %d", i, e.Idx)
		}
	}
	if !strings.Contains(mid[0].Msg, "2500") {
		t.Errorf("mid[0].Msg = %q", mid[0].Msg)
	}

	// đọc cuối
	tail, err := ReadEntries(ix, 4998, 10)
	if err != nil || len(tail) != 2 {
		t.Fatalf("Read(4998,10) = %d entries, err=%v", len(tail), err)
	}
	if tail[1].Idx != 4999 {
		t.Errorf("tail[1].Idx = %d", tail[1].Idx)
	}

	// entry có continuation: entry 0 có 2 dòng stacktrace
	e0, _ := ReadEntries(ix, 0, 1)
	if e0[0].Msg == "" || !strings.Contains(e0[0].Msg, "\n\tat com.example.Foo.bar") {
		t.Errorf("entry0 phải chứa continuation, msg=%q", e0[0].Msg)
	}
	if !strings.Contains(e0[0].Raw, "2026-09-15") {
		t.Errorf("raw phải chứa header")
	}
}

func TestSearch(t *testing.T) {
	p := writeTestFile(t, buildContent(1000))
	ix, err := BuildIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	var hits []int64
	Search(ix, "mesage so 09", "", func(local int64, e Entry) bool {
		hits = append(hits, local)
		return true
	})
	// i = 90..99 → 10 hits
	if len(hits) != 10 {
		t.Errorf("hits = %d, muốn 10: %v", len(hits), hits)
	}
	// dừng sớm
	count := 0
	Search(ix, "mesage so", "", func(int64, Entry) bool {
		count++
		return count < 5
	})
	if count != 5 {
		t.Errorf("dừng sớm không hoạt động: %d", count)
	}
}
