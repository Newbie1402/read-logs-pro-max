# Log Viewer

Ứng dụng đọc log từ MinIO, chạy local trên **macOS** và **Windows**, giao diện web tối giản.
Một file nhị phân duy nhất ~9 MB, không cần cài đặt gì thêm, RAM chỉ chiếm vài chục MB.

> Dành cho hệ thống cần xem log hằng ngày (cấu trúc bucket theo ngày/giờ) mà không muốn mở Console của MinIO + giải nén thủ công mỗi lần.

---

## Tính năng

### Kết nối MinIO
- **Đăng nhập bằng trang MinIO Console** — chỉ cần URL console + username/password (giống lúc đăng nhập web), không cần tự tìm access key/secret key.
- **Tự dò S3 API** (cùng domain hoặc cổng 9000) và lưu lại để lần sau kết nối ngay.
- **Chế độ dự phòng qua Console**: nếu VPS chỉ proxy console ra ngoài (S3 API không public), app tự chuyển sang dùng session console để liệt kê/tải log — không cần sửa gì trên server.
- Dán cả link `/browser/<bucket>` vào ô URL là app tự nhận bucket; hoặc bấm **Lấy danh sách** để chọn từ danh sách bucket của user.
- Mở được cả file `.tar.gz` **đã tải sẵn trên máy**.

### Xem log
- Chọn khoảng ngày + nút nhanh **24 giờ / 7 ngày / 30 ngày** → Quét → Tải & mở. File gộp theo đúng thứ tự thời gian.
- Trình đọc **virtualized** — vài triệu dòng vẫn cuộn mượt (chỉ render phần đang nhìn).
- **Tự xuống dòng** bật/tắt (như Option+Z trong VSCode) — phím tắt `Alt+Z` / `⌥Z`.
- **Giờ hiển thị theo giờ máy (UTC+7)** hoặc UTC gốc, đổi 1 bấm.
- Level tô màu: ERROR / WARN / INFO / DEBUG; dòng ERROR có nền gợn đỏ nhẹ.
- Stack trace / exception được gộp vào đúng dòng log cha.
- **Kéo giãn từng cột** (thời gian, thread, logger…) — độ rộng được ghi nhớ.
- **Tìm kiếm** không phân biệt hoa thường + lọc theo level, từ khớp được **tô nổi** ngay trong dòng.
- **Nhảy tới dòng #** bất kỳ, nút Về cuối, copy nguyên dòng bằng cách bấm vào số dòng.

### JSON trong log
- **Rê chuột** vào đoạn `{...}` → xem nhanh ở panel bên phải.
- **Bấm** vào đoạn → mở **popup** giữa màn hình.
- 2 chế độ xem **Bảng** (khóa/giá trị) hoặc **JSON** (format có màu) — lựa chọn được ghi nhớ.
- Ghim panel, copy JSON gốc.

### Quản lý bộ nhớ
- **Stream từ MinIO, giải nén trực tiếp** — mặc định không lưu file `.tar.gz` về đĩa, chỉ giữ log đã giải nén trong thư mục cache.
- File đã tải rồi → lần mở sau **không tải lại**.
- Màn hình **Cache**: xem dung lượng từng ngày, xóa từng ngày / xóa hết, mở thư mục cache trong Finder/Explorer.
- **Tự dọn** cache cũ hơn N ngày khi mở app (mặc định 14, cấu hình được).
- Chọn thư mục cache bằng **dialog của máy** (Finder/Explorer), không cần gõ tay.

---

## Chạy app

### 1. Cấu hình

Điền thông tin vào file `.env` cạnh file chạy (hoặc mở app và nhập trong màn hình Cài đặt — app tự lưu lại):

```env
# URL trang MinIO Console (dán cả link /browser/<bucket> cũng được)
MINIO_CONSOLE_URL=https://minio.uongoc.vn
# URL trang đăng nhập (tùy chọn, nếu khác URL chính)
MINIO_LOGIN_URL=https://minio.uongoc.vn/login
# Tên đăng nhập + mật khẩu của trang MinIO Console
MINIO_USERNAME=xxxxx
MINIO_PASSWORD=xxxxx
# Bucket chứa log (để trống thì app tự lấy danh sách bucket để chọn)
MINIO_BANK_TRANSACTION_BUCKET=accounting-file-logs

# S3 API endpoint — KHÔNG cần điền: app tự dò và lưu lại sau lần đầu
MINIO_URL=
```

Xem `.env.example` cho đầy đủ các tùy chọn (`PORT`, `CACHE_DIR`, `KEEP_ARCHIVES`, `AUTO_CLEAN_DAYS`).

### 2. Khởi động

- **macOS**: mở Terminal, kéo file `LogViewer-macOS-arm64` (Apple Silicon) hoặc `LogViewer-macOS-amd64` (Intel) vào cửa sổ, Enter.
- **Windows**: double-click `LogViewer-Windows-amd64.exe`.

Trình duyệt tự mở tại `http://localhost:8765` (đổi cổng bằng `PORT` trong `.env`).

> Lần đầu chạy trên macOS nếu bị chặn Gatekeeper:
> ```sh
> xattr -d com.apple.quarantine LogViewer-macOS-arm64
> ```

### 3. Cấu trúc bucket được hỗ trợ

```
<bucket>/
  2026-09-30/
    accountant_logs_2026-09-30_07-00.tar.gz   <- phần đuôi 07 là giờ
    accountant_logs_2026-09-30_08-00.tar.gz
  2026-10-01/
    ...
```

Trong mỗi archive có thể chứa file `.log` và file `.gz` (log rotate) ở bất kỳ thư mục con nào — app giải nén hết và chỉ giữ file log/text.

---

## Phím tắt & tương tác nhanh

| Thao tác | Chức năng |
|---|---|
| `Alt+Z` (⌥Z trên Mac) | Bật/tắt tự xuống dòng |
| `Ctrl/Cmd+F` | Focus ô tìm kiếm |
| `Esc` | Đóng modal / popup |
| Bấm **số dòng** | Copy nguyên dòng log |
| Bấm vào đoạn `{...}` | Mở popup JSON |
| Rê chuột vào `{...}` | Xem nhanh JSON ở panel phải |
| Kéo mép cột ở header | Đổi độ rộng cột |
| Bấm vào dòng | Tô sáng dòng để theo dõi |

---

## Bộ nhớ cache — đang lưu gì, ở đâu?

Mặc định cache nằm ở thư mục `log-cache/` cạnh file chạy (đổi được trong Cài đặt):

```
log-cache/
  extracted/2026-09-30/07-accountant_logs_.../accountant-service.log ...
  archives/          <- chỉ có nếu bật "giữ lại file .tar.gz"
  imported/          <- log từ file .tar.gz mở local
  tmp/               <- file tạm, tự dọn khi mở app
```

Mọi thứ xóa được an toàn trong app — file gốc trên MinIO không bị ảnh hưởng.

---

## Build từ nguồn

Yêu cầu: Go >= 1.22 (`brew install go`).

```sh
./build.sh       # build dist/ cho macOS arm64 + amd64 và Windows amd64
go test ./...    # chạy unit test
go vet ./...     # kiểm tra tĩnh
```

Toàn bộ frontend được nhúng vào binary (không cần web server hay Node.js gì thêm).
Lưu ý: `build.sh` không ghi đè `dist/.env` đã có sẵn, và `.env` (chứa mật khẩu) nằm trong `.gitignore` — không bao giờ commit.

---

## Kiến trúc (cho ai muốn sửa code)

```
main.go                  khởi động, mở trình duyệt, dọn cache lúc bật
internal/config          đọc/ghi .env
internal/s3x             client MinIO:
                           - client.go     S3 API trực tiếp (minio-go)
                           - console.go    chế độ qua Console API + tự dò endpoint
internal/store           cache: giải nén stream (tar.gz + .gz lồng), manifest, dọn rác
internal/logs            parse log Spring Boot, index theo block 64KB (ít RAM), đọc/search bất kỳ dòng
internal/server          HTTP API + phục vụ UI nhúng
web/                     giao diện vanilla JS (không framework), virtualized list tự viết
```

Nguyên tắc hoạt động chính:
- **Tải**: stream từng archive → giải nén member `.log` / gunzip `.gz` lồng → ghi vào cache `extracted/<ngày>/<giờ>-...` → ghi manifest `.done.json` (dùng để bỏ qua lần sau).
- **Index**: mỗi file log quét 1 lần, lưu mốc 64KB (số dòng + offset) → RAM chỉ vài trăm KB/file lớn, đọc dòng bất kỳ bằng seek.
- **Viewer**: chia block 100 dòng, chỉ render block đang nhìn; chiều cao đo thật rồi cache lại.

## Xử lý sự cố

| Triệu chứng | Cách xử lý |
|---|---|
| "Sai tên đăng nhập hoặc mật khẩu" | Kiểm tra user/pass đăng nhập trang Console |
| "Không tìm được S3 API nào chạy được" | Bình thường nếu VPS không expose cổng 9000 — app sẽ tự chuyển qua chế độ Console |
| "Liệt kê object trả về HTTP ..." (chế độ Console) | Phiên bản MinIO Console của bạn có thể có API khác — gửi kèm thông báo lỗi (chứa phản hồi thật từ server) để sửa parser |
| Quét ra số file ít hơn trên Console | Kiểm tra khoảng ngày; chế độ Console liệt kê theo từng ngày — nếu bucket quá lớn có thể chậm, thu hẹp khoảng |
| Cổng 8765 bị chiếm | Đổi `PORT` trong `.env` hoặc chạy `./LogViewer-macOS-arm64 -port 9001` |
| Không chọn được file khi "Mở file .tar.gz" | Dùng Chrome/Edge/Safari bản mới, hoặc gõ đường dẫn file vào ô nhập của dialog |
