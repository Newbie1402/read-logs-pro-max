package logs

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const chunkSize = 64 * 1024

// Chunk: tại offset Off, entry kế tiếp bắt đầu từ (hoặc sau) đó có số thứ tự N.
// Dùng để seek nhanh tới entry bất kỳ mà không cần index từng dòng (tiết kiệm RAM).
type Chunk struct {
	N   int64
	Off int64
}

type FileIndex struct {
	Path    string  `json:"-"`
	Name    string  `json:"name"`
	Size    int64   `json:"size"`
	Entries int64   `json:"entries"`
	Chunks  []Chunk `json:"-"`
	modTime time.Time
	mu      sync.Mutex
}

// BuildIndex quét file 1 lần: đếm entry và ghi các mốc 64KB (RAM ~ vài trăm KB/file lớn).
func BuildIndex(path string) (*FileIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	ix := &FileIndex{Path: path, Size: st.Size(), modTime: st.ModTime()}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		ix.Name = path[i+1:]
	} else if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		ix.Name = path[i+1:]
	} else {
		ix.Name = path
	}

	r := bufio.NewReaderSize(f, 1<<20)
	var off, entry int64
	nextChunk := int64(chunkSize)
	empty := true
	for {
		line, err := r.ReadString('\n')
		if lineStart := off; lineStart >= nextChunk {
			ix.Chunks = append(ix.Chunks, Chunk{N: entry, Off: lineStart})
			for nextChunk <= lineStart {
				nextChunk += chunkSize
			}
		}
		off += int64(len(line))
		if empty && strings.TrimSpace(line) != "" {
			empty = false
		}
		if IsStart(strings.TrimRight(line, "\r\n")) {
			entry++
		}
		if err != nil {
			break
		}
	}
	if empty {
		ix.Entries = 0
	} else {
		ix.Entries = entry
	}
	return ix, nil
}

// reader đọc entry từ file, phục vụ cả Read lẫn Search.
type entryReader struct {
	f    *os.File
	r    *bufio.Reader
	ix   *FileIndex
	off  int64 // byte offset hiện tại của r
	rest string // dòng dư từ lần đọc trước
}

func openEntryReader(ix *FileIndex, seek int64) (*entryReader, error) {
	f, err := os.Open(ix.Path)
	if err != nil {
		return nil, err
	}
	er := &entryReader{f: f, ix: ix}
	if seek > 0 {
		if _, err := f.Seek(seek, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
		er.r = bufio.NewReaderSize(f, 1<<20)
		er.off = seek
	} else {
		er.r = bufio.NewReaderSize(f, 1<<20)
	}
	return er, nil
}

func (er *entryReader) Close() { er.f.Close() }

// nextLine trả về dòng kế tiếp (không gồm \n); hết file → ok=false.
func (er *entryReader) nextLine() (string, bool) {
	if er.rest != "" {
		l := er.rest
		er.rest = ""
		return l, true
	}
	line, err := er.r.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	er.off += int64(len(line))
	return strings.TrimRight(line, "\r\n"), true
}

type rawEntry struct {
	lines []string
}

// scanFrom quét các entry bắt đầu từ vị trí hiện tại; mỗi entry hoàn chỉnh gọi cb
// (cb trả false để dừng). Trả về số entry đã đóng. Lưu ý: nếu reader được mở với
// seek > 0, entry đầu tiên có thể dở dang (thiếu phần đầu) — caller tự xử lý.
func (er *entryReader) scanFrom(cb func(e rawEntry) bool) int {
	n := 0
	var cur rawEntry
	open := false
	for {
		line, ok := er.nextLine()
		if ok && IsStart(line) {
			if open {
				if !cb(cur) {
					return n
				}
				n++
			}
			cur = rawEntry{lines: []string{line}}
			open = true
		} else if ok && open {
			cur.lines = append(cur.lines, line)
		}
		if !ok {
			if open {
				if !cb(cur) {
					return n
				}
				n++
			}
			return n
		}
	}
}

// toEntry chuyển rawEntry → Entry (giữ Idx=0; caller gán chỉ số toàn cục).
func toEntry(re rawEntry) Entry {
	header := re.lines[0]
	ts, level, _, thread, logger, msg, ok := ParseHeader(header)
	var disp, raw string
	if ok {
		var b strings.Builder
		b.WriteString(msg)
		for _, l := range re.lines[1:] {
			b.WriteByte('\n')
			b.WriteString(l)
		}
		disp = b.String()
		raw = strings.Join(re.lines, "\n")
	} else {
		disp = strings.Join(re.lines, "\n")
		raw = disp
	}
	return Entry{TS: ts, Level: level, Thread: thread, Logger: logger, Msg: disp, Raw: raw}
}

// ReadEntries đọc tối đa count entry bắt đầu từ localFrom (0-based) trong file này.
func ReadEntries(ix *FileIndex, localFrom, count int64) ([]Entry, error) {
	if count <= 0 || localFrom < 0 || localFrom >= ix.Entries {
		return nil, nil
	}
	seek, base := chunkFor(ix, localFrom)
	er, err := openEntryReader(ix, seek)
	if err != nil {
		return nil, err
	}
	defer er.Close()

	out := make([]Entry, 0, count)
	idx := base // số thứ tự entry kế tiếp sẽ được đóng (nếu entry đầu bị dở dang thì bị bỏ qua)
	first := true
	er.scanFrom(func(re rawEntry) bool {
		if first && seek > 0 && !IsStart(re.lines[0]) {
			first = false
			return true // bỏ entry dở dang do seek
		}
		first = false
		if idx >= localFrom {
			e := toEntry(re)
			e.Idx = idx
			out = append(out, e)
			if int64(len(out)) >= count {
				return false
			}
		}
		idx++
		return true
	})
	return out, nil
}

func chunkFor(ix *FileIndex, local int64) (seek int64, entryAt int64) {
	seek, entryAt = 0, 0
	lo, hi := 0, len(ix.Chunks)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if ix.Chunks[mid].N <= local {
			seek, entryAt = ix.Chunks[mid].Off, ix.Chunks[mid].N
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return seek, entryAt
}

// Search quét toàn file, trả về chỉ số entry (trong file) khớp q (chứa, không phân biệt hoa thường)
// và/hoặc đúng level. Gọi cb cho từng kết quả; cb trả false để dừng.
func Search(ix *FileIndex, q, level string, cb func(local int64, e Entry) bool) error {
	er, err := openEntryReader(ix, 0)
	if err != nil {
		return err
	}
	defer er.Close()
	wantLevel := strings.ToUpper(level)
	qLower := strings.ToLower(q)
	local := int64(0)
	er.scanFrom(func(re rawEntry) bool {
		e := toEntry(re)
		match := true
		if wantLevel != "" && wantLevel != "ALL" {
			match = e.Level == wantLevel
		}
		if match && qLower != "" {
			match = strings.Contains(strings.ToLower(e.Raw), qLower)
		}
		if match {
			if !cb(local, e) {
				return false
			}
		}
		local++
		return true
	})
	return nil
}

// ---------- search nhanh cho file lớn (hàng triệu dòng) ----------

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

// containsFold: chứa sub (không phân biệt hoa thường ASCII), KHÔNG cấp phát.
// Dò byte đầu bằng IndexByte (SIMD) rồi so phần còn lại — nhanh gấp nhiều lần
// so với strings.Contains(strings.ToLower(s)) vốn cấp phát cho từng dòng.
func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	n := len(sub)
	if n > len(s) {
		return false
	}
	l0, u0 := lowerByte(sub[0]), upperByte(sub[0])
	for i := 0; i+n <= len(s); {
		j := -1
		if l0 != u0 {
			jl := strings.IndexByte(s[i:], l0)
			ju := strings.IndexByte(s[i:], u0)
			switch {
			case jl < 0:
				j = ju
			case ju < 0:
				j = jl
			case jl < ju:
				j = jl
			default:
				j = ju
			}
		} else {
			j = strings.IndexByte(s[i:], l0)
		}
		if j < 0 || i+j+n > len(s) {
			return false
		}
		if foldEq(s[i+j:i+j+n], sub) {
			return true
		}
		i += j + 1
	}
	return false
}

func upperByte(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func foldEq(a, b string) bool {
	for k := 0; k < len(b); k++ {
		if lowerByte(a[k]) != lowerByte(b[k]) {
			return false
		}
	}
	return true
}

// SearchScan: quét tối ưu — kiểm tra khớp theo từng dòng thô (không cấp phát),
// chỉ build Entry khi entry thực sự khớp; Raw để rỗng (kết quả search gọn,
// nội dung đầy đủ fetch sau qua /entries). ctx cho phép hủy giữa chừng.
func SearchScan(ix *FileIndex, q, level string, ctx context.Context, cb func(local int64, e Entry) bool) error {
	er, err := openEntryReader(ix, 0)
	if err != nil {
		return err
	}
	defer er.Close()

	wantLevel := strings.ToUpper(level)
	mustLv := wantLevel != "" && wantLevel != "ALL"
	qLower := strings.ToLower(q)
	mustQ := qLower != ""

	var buf []string // dòng của entry đang mở (tái sử dụng)
	lvOK := false
	qOK := false
	started := false // entry hiện tại có bắt đầu bằng dòng header hay không
	local := int64(0)

	flush := func() bool {
		if len(buf) > 0 && started {
			if (lvOK || !mustLv) && (qOK || !mustQ) {
				e := buildEntry(buf)
				if !cb(local, e) {
					return false
				}
			}
			local++
		}
		buf = buf[:0]
		started = false
		lvOK = false
		qOK = false
		return true
	}

	for {
		if local&1023 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		line, ok := er.nextLine()
		if ok && IsStart(line) {
			if len(buf) > 0 {
				if !flush() {
					return nil
				}
			}
			buf = append(buf, line)
			started = true
			if mustLv {
				_, lv, _, _, _, _, okh := ParseHeader(line)
				lvOK = okh && lv == wantLevel
			}
			if mustQ {
				qOK = containsFold(line, qLower)
			}
		} else if ok {
			if started {
				buf = append(buf, line)
				if mustQ && !qOK {
					qOK = containsFold(line, qLower)
				}
			}
			// dòng rác trước entry đầu tiên: bỏ qua
		}
		if !ok {
			if !flush() {
				return nil
			}
			return nil
		}
	}
}

// buildEntry: entry từ các dòng đã thu thập (chỉ gọi khi entry khớp).
// Raw rỗng — dùng cho kết quả search (payload gọn); hiển thị chỉ cần Msg.
func buildEntry(lines []string) Entry {
	header := lines[0]
	ts, level, _, thread, logger, msg, ok := ParseHeader(header)
	var disp string
	if ok {
		var b strings.Builder
		b.WriteString(msg)
		for _, l := range lines[1:] {
			b.WriteByte('\n')
			b.WriteString(l)
		}
		disp = b.String()
	} else {
		disp = strings.Join(lines, "\n")
	}
	return Entry{TS: ts, Level: level, Thread: thread, Logger: logger, Msg: disp}
}

// Session: tập nhiều file đã gộp, sắp xếp theo (date, hour, tên file).
type Session struct {
	ID         string
	Files      []*FileIndex
	Cut        []int64 // Cut[i] = số entry trước file i
	Total      int64
	TotalBytes int64
	Meta       []FileMeta
}

type FileMeta struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Date    string `json:"date"`
	Hour    int    `json:"hour"`
	Entries int64  `json:"entries"`
	Size    int64  `json:"size"`
}

func NewSession(id string, files []*FileIndex, metas []FileMeta) *Session {
	s := &Session{ID: id, Files: files, Meta: metas}
	s.Cut = make([]int64, len(files)+1)
	for i, f := range files {
		s.Cut[i+1] = s.Cut[i] + f.Entries
	}
	s.Total = s.Cut[len(files)]
	return s
}

func (s *Session) locate(global int64) (int, int64) {
	lo, hi := 0, len(s.Files)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if s.Cut[mid] <= global {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, global - s.Cut[lo]
}

// Read đọc count entry toàn cục bắt đầu từ from.
func (s *Session) Read(from, count int64) []Entry {
	if count <= 0 || from < 0 || from >= s.Total {
		return nil
	}
	var out []Entry
	for from < s.Total && int64(len(out)) < count {
		fi, local := s.locate(from)
		batch, err := ReadEntries(s.Files[fi], local, count-int64(len(out)))
		if err != nil || len(batch) == 0 {
			break
		}
		for i := range batch {
			batch[i].Idx = s.Cut[fi] + batch[i].Idx
		}
		out = append(out, batch...)
		from += int64(len(batch))
	}
	return out
}

// Search toàn session; trả về tối đa maxResult entry khớp (kèm nội dung) + tổng số khớp.
// Dùng SearchScan (quét nhanh không cấp phát cho từng dòng) — Raw rỗng để payload gọn;
// nội dung đầy đủ của dòng nào cần copy thì fetch riêng qua /entries.
func (s *Session) Search(q, level string, maxResult int, ctx context.Context) (matches []Entry, total int64, truncated bool, err error) {
	matches = []Entry{}
	for fi, f := range s.Files {
		base := s.Cut[fi]
		err = SearchScan(f, q, level, ctx, func(local int64, e Entry) bool {
			total++
			if int64(len(matches)) < int64(maxResult) {
				e.Idx = base + local
				matches = append(matches, e)
			}
			return true
		})
		if err != nil {
			return
		}
	}
	truncated = total > int64(maxResult)
	return
}
