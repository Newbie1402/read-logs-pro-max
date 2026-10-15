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

---

## Deploy lên VPS (cho cả team dùng chung)

Nhánh `vps-deploy` chứa bản tối ưu cho chạy nền web: bind ngoài + khóa bằng mật khẩu + build Linux.

### 1. Build & đẩy file lên server

```sh
git checkout vps-deploy
./build.sh          # có thêm LogViewer-linux-amd64 / -arm64 trong dist/
```

Copy 2 file lên VPS (thay IP của bạn):

```sh
scp dist/LogViewer-linux-amd64 deploy/logviewer.service root@<IP-VPS>:/tmp/
ssh root@<IP-VPS> "
  mkdir -p /opt/logviewer && mv /tmp/LogViewer-linux-amd64 /opt/logviewer/ &&
  useradd -r -s /usr/sbin/nologin logviewer 2>/dev/null;
  cp /tmp/logviewer.service /etc/systemd/system/ && nano /opt/logviewer/.env
"
```

### 2. File `.env` trên VPS

```env
MINIO_CONSOLE_URL=https://minio.uongoc.vn
MINIO_LOGIN_URL=https://minio.uongoc.vn/login
MINIO_USERNAME=xxxx
MINIO_PASSWORD=xxxx
MINIO_BANK_TRANSACTION_BUCKET=accounting-file-logs

HOST=0.0.0.0          # cho phép truy cập từ ngoài
APP_PASSWORD=matkhau-team   # BẮT BUỘC đặt khi để public
AUTO_CLEAN_DAYS=14
```

### 3. Chạy như service

```sh
chmod +x /opt/logviewer/LogViewer-linux-amd64
chown -R logviewer:logviewer /opt/logviewer
systemctl daemon-reload
systemctl enable --now logviewer
journalctl -u logviewer -f     # xem log app
```

App chạy ở cổng 8765. Đặt nginx phía trước để có HTTPS + domain:

```nginx
server {
    listen 443 ssl;
    server_name logs.uongoc.vn;
    # ssl_certificate ...; ssl_certificate_key ...;

    location / {
        proxy_pass http://127.0.0.1:8765;
        proxy_set_header Host $host;
        proxy_read_timeout 300s;    # tải archive lớn
        proxy_buffering off;        # stream log mượt hơn
    }
}
```

> **Bảo mật**: `APP_PASSWORD` khóa toàn bộ API (ai không có mật khẩu chỉ thấy trang nhập khẩu). Cookie phiên sống 30 ngày. Nếu chỉ dùng nội bộ qua VPN/WireGuard thì vẫn nên đặt để chặn scan bừa.

### Deploy bằng Docker Compose

Trên máy deploy, tạo file cấu hình và khởi động:

```sh
cp .env.example .env
# Điền MINIO_* và APP_PASSWORD trong .env
docker compose up -d --build
```

App sẽ chạy ở `http://<server>:8765`. Cache và file cấu hình được giữ trong
volume `logviewer-data` khi container được build hoặc tạo lại. Đổi cổng public bằng
`LOGVIEWER_PORT`, ví dụ `LOGVIEWER_PORT=9001 docker compose up -d`.

Xem log hoặc dừng app:

```sh
docker compose logs -f logviewer
docker compose down
```

### Deploy production bằng Docker Swarm + GitHub Actions

Workflow production chạy khi có push vào nhánh `prod`. Nhánh làm việc dùng
`feat/...` hoặc `fix/...`, mở PR vào `dev`; sau khi `dev` được merge/promote
sang `prod`, GitHub Actions sẽ build image theo commit, push lên GHCR và
deploy stack với tag immutable `sha-<commit>`.

Trên Swarm manager, chuẩn bị một lần:

```sh
sudo mkdir -p /opt/logviewer/data
sudo docker swarm init # chỉ chạy nếu máy chưa là Swarm manager
```

Tạo GitHub Environment `production` và các secrets sau:

| Secret | Nội dung |
|---|---|
| `DEPLOY_HOST` | Host/IP của Swarm manager |
| `DEPLOY_USER` | User SSH có quyền dùng Docker |
| `DEPLOY_SSH_KEY` | Private key deploy |
| `DEPLOY_KNOWN_HOSTS` | Kết quả `ssh-keyscan -H <host>` |
| `TAILSCALE_AUTHKEY` | Auth key để GitHub Actions vào tailnet |
| `GHCR_USERNAME` | User GitHub/PAT owner package |
| `GHCR_TOKEN` | PAT có quyền đọc package GHCR trên VPS |

GitHub Actions không lưu, upload hoặc ghi đè `.env`. Hãy tạo file cố định trên
VPS tại `/opt/logviewer/data/.env` trước lần deploy đầu tiên:

```env
HOST=0.0.0.0
PORT=8765
CACHE_DIR=/data/log-cache
APP_PASSWORD=doi-mat-khau-nay
MINIO_CONSOLE_URL=https://minio.example.com
MINIO_USERNAME=...
MINIO_PASSWORD=...
MINIO_BANK_TRANSACTION_BUCKET=...
AUTO_CLEAN_DAYS=14
KEEP_ARCHIVES=false
```

Phân quyền file cấu hình:

```sh
sudo mkdir -p /opt/logviewer/data
sudo chmod 700 /opt/logviewer/data
sudo chmod 600 /opt/logviewer/data/.env
```

Stack dùng bind mount `/opt/logviewer/data:/data`, nên cache, `.env` và config
được giữ nguyên qua mọi lần deploy/rollback trên VPS. Service chỉ chạy một
replica trên manager vì cache hiện là dữ liệu local; log container được Docker
rotate ở mức 10 MB/file, giữ 5 file. Healthcheck dùng `/healthz`, còn
`APP_PASSWORD` vẫn bảo vệ toàn bộ API nghiệp vụ.

Stack Swarm truyền các biến từ file `.env` cố định trên VPS vào container. Khi
deploy bằng workflow, VPS tự pull image từ GHCR rồi Docker Compose plugin đọc
`/opt/logviewer/data/.env` để render stack trước khi đưa vào Swarm; vì vậy máy
manager cần có lệnh `docker compose` (không chỉ `docker stack`). Nếu dùng
Tailscale, đặt `DEPLOY_HOST` là IP Tailscale của Swarm manager và cho phép SSH
qua tailnet; workflow sẽ kết nối Tailscale trước lệnh SSH/SCP đầu tiên.

Không cần chạy lệnh deploy thủ công; chỉ cần merge code từ `dev` vào `prod`.

### 4. Hiệu năng với log lớn (đã tối ưu sẵn)

- Trình đọc **virtualized + phân đoạn**: trình duyệt chỉ render ~300 dòng đang nhìn; session lớn chia thành đoạn 300.000 dòng, cuộn tới đáy tự nạp đoạn kế — chiều cao cuộn luôn dưới giới hạn của mọi trình duyệt (Firefox/Safari chỉ chịu ~17M px).
- **Tìm kiếm** quét ~300MB/1 triệu dòng bằng thuật toán không cấp phát từng dòng, trả về tối đa 20.000 kết quả dạng gọn — server ăn ít RAM, không nghẽn GC.
- Index theo block 64KB: đọc dòng bất kỳ là seek + quét cục bộ, không load nguyên file vào RAM.
- Cache trên đĩa, giới hạn `MemoryMax=1G` trong systemd đề phòng.

Số liệu đo thực tế (Mac M1, **1.000.000 dòng / 210MB text / 17MB nén**):

| Thao tác | Kết quả |
|---|---|
| Giải nén + index | **0.39s** |
| Đọc 50 dòng tại vị trí bất kỳ | ~1.3ms |
| Tìm từ khóa quét hết 1 triệu dòng | **0.12–0.2s** |
| Tìm theo level (55.556 khớp, trả 20.000) | 0.21s |
| RAM server (sau index + nhiều lần search) | 60–105 MB |
| DOM trình duyệt khi cuộn 1 triệu dòng | ~300 dòng (không tăng) |

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
