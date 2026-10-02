// Package logs: parse dòng log Spring Boot, đánh index và đọc/search theo entry.
package logs

import "strings"

// Entry là một bản ghi log (1 dòng header + các dòng tiếp nối như stack trace).
type Entry struct {
	Idx    int64  `json:"i"`
	TS     string `json:"ts,omitempty"`  // 2026-09-15T14:49:24.108Z
	Level  string `json:"lv,omitempty"`  // INFO/WARN/ERROR/DEBUG...
	App    string `json:"-"`
	Thread string `json:"th,omitempty"`
	Logger string `json:"lg,omitempty"`
	Msg    string `json:"m"`  // toàn bộ nội dung hiển thị (không gồm header cột)
	Raw    string `json:"raw"` // văn bản gốc đầy đủ (copy)
}

// IsStart: dòng có timestamp ở đầu → bắt đầu 1 entry mới; ngược lại là dòng tiếp nối.
func IsStart(line string) bool {
	if len(line) < 19 {
		return false
	}
	for i, ch := range line[:19] {
		switch i {
		case 4, 7:
			if ch != '-' {
				return false
			}
		case 10:
			if ch != 'T' {
				return false
			}
		case 13, 16:
			if ch != ':' {
				return false
			}
		default:
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

// ParseHeader phân tích dòng header dạng:
// 2026-09-15T14:49:24.108Z  INFO 1 --- [evc-accounting] [AsyncJobBankMigrationThread-5206] c.e.e.s.b.p.SMS.SmsLegacyApiClient       : msg...
func ParseHeader(line string) (ts, level, app, thread, logger, msg string, ok bool) {
	if !IsStart(line) {
		return "", "", "", "", "", "", false
	}
	// token đầu tiên là timestamp đầy đủ (kèm mili-giây + Z)
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return "", "", "", "", "", "", false
	}
	ts = line[:sp]
	rest := strings.TrimLeft(line[sp:], " ")
	// level
	i := strings.IndexByte(rest, ' ')
	if i <= 0 {
		return "", "", "", "", "", "", false
	}
	level = rest[:i]
	rest = strings.TrimLeft(rest[i:], " ")
	// pid
	j := strings.IndexByte(rest, ' ')
	if j <= 0 {
		return "", "", "", "", "", "", false
	}
	rest = strings.TrimLeft(rest[j:], " ")
	// "---"
	if !strings.HasPrefix(rest, "---") {
		return "", "", "", "", "", "", false
	}
	rest = strings.TrimLeft(rest[3:], " ")
	// các nhóm [..]: nhóm đầu là app, nhóm cuối là thread
	var groups []string
	for strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			break
		}
		groups = append(groups, rest[1:end])
		rest = strings.TrimLeft(rest[end+1:], " ")
	}
	if len(groups) > 0 {
		app = groups[0]
		thread = groups[len(groups)-1]
	}
	// logger ... : msg
	if k := strings.Index(rest, " : "); k >= 0 {
		logger = strings.TrimSpace(rest[:k])
		msg = rest[k+3:]
	} else if k = strings.Index(rest, ": "); k >= 0 {
		logger = strings.TrimSpace(rest[:k])
		msg = rest[k+2:]
	} else {
		msg = rest
	}
	return ts, level, app, thread, logger, msg, true
}
