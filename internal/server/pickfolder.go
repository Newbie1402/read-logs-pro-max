package server

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
)

// handlePickFolder mở dialog chọn thư mục gốc của hệ điều hành (Finder / Explorer
// / zenity) ngay trên máy đang chạy app và trả về đường dẫn được chọn.
func (a *App) handlePickFolder(w http.ResponseWriter, r *http.Request) {
	path, canceled, err := pickFolder()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if canceled {
		writeJSON(w, 200, map[string]any{"canceled": true})
		return
	}
	writeJSON(w, 200, map[string]any{"path": path})
}

func pickFolder() (path string, canceled bool, err error) {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("osascript", "-e",
			`POSIX path of (choose folder with prompt "Chọn thư mục cache cho Log Viewer")`)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, runErr := cmd.Output()
		if runErr != nil {
			if strings.Contains(stderr.String(), "canceled") || strings.Contains(stderr.String(), "Cancelled") {
				return "", true, nil
			}
			return "", false, fmt.Errorf("mở dialog chọn thư mục lỗi: %v (%s)", runErr, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), false, nil

	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms | Out-Null; ` +
			`$f = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$f.Description = 'Chọn thu muc cache cho Log Viewer'; ` +
			`$f.ShowNewFolderButton = $true; ` +
			`if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $f.SelectedPath }`
		out, runErr := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script).Output()
		if runErr != nil {
			return "", false, fmt.Errorf("mở dialog chọn thư mục lỗi: %v", runErr)
		}
		p := strings.TrimSpace(string(out))
		if p == "" {
			return "", true, nil
		}
		return p, false, nil

	default: // linux
		cmd := exec.Command("zenity", "--file-selection", "--directory", "--title=Chọn thư mục cache cho Log Viewer")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, runErr := cmd.Output()
		if runErr != nil {
			if ee, ok := runErr.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return "", true, nil
			}
			return "", false, fmt.Errorf("cần cài zenity để mở dialog chọn thư mục: %v", runErr)
		}
		return strings.TrimSpace(string(out)), false, nil
	}
}
