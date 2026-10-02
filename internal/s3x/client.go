// Package s3x: client MinIO/S3 tối giản cho nhu cầu app — liệt kê & tải object.
package s3x

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Object struct {
	Key  string `json:"key"`
	Date string `json:"date"` // yyyy-mm-dd (từ folder)
	Hour int    `json:"hour"` // giờ trong tên file, -1 nếu không xác định
	Size int64  `json:"size"`
}

type Client struct {
	cli    *minio.Client
	bucket string
}

var dateDirRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})/$`)
var hourRe = regexp.MustCompile(`_(\d{2})-?\d{0,2}\.tar\.gz$`)

// New tạo client từ cấu hình. MINIO_URL có thể có/không có scheme.
func New(rawURL, access, secret, bucket string) (*Client, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil, fmt.Errorf("MINIO_URL trống")
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	pu, err := url.Parse(u)
	if err != nil {
		return nil, fmt.Errorf("MINIO_URL không hợp lệ: %v", err)
	}
	if pu.Host == "" {
		return nil, fmt.Errorf("MINIO_URL thiếu host: %s", rawURL)
	}
	cli, err := minio.New(pu.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: pu.Scheme == "https",
	})
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, bucket: bucket}, nil
}

// Test kiểm tra bucket truy cập được và trả về số thư mục ngày.
func (c *Client) Test(ctx context.Context) (int, error) {
	if c.bucket == "" {
		// chưa chọn bucket: chỉ cần liệt kê được bucket là coi như kết nối OK
		bs, err := c.ListBuckets(ctx)
		return len(bs), err
	}
	ok, err := c.cli.BucketExists(ctx, c.bucket)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("bucket %q không tồn tại hoặc không có quyền", c.bucket)
	}
	dates, err := c.ListDates(ctx)
	return len(dates), err
}

// ListDates trả về các thư mục ngày (yyyy-mm-dd) có trong bucket.
func (c *Client) ListDates(ctx context.Context) ([]string, error) {
	out := []string{}
	for obj := range c.cli.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Recursive: false}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		if m := dateDirRe.FindStringSubmatch(obj.Key); m != nil {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListRange liệt kê toàn bộ file .tar.gz trong khoảng [from..to] (định dạng yyyy-mm-dd).
// fromHour/toHour (0..23) áp dụng cho ngày đầu/ngày cuối để hỗ trợ cửa sổ 24h; truyền -1 để bỏ qua.
func (c *Client) ListRange(ctx context.Context, from, to string, fromHour, toHour int) ([]Object, error) {
	dates, err := datesBetween(from, to)
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, date := range dates {
		prefix := date + "/"
		for obj := range c.cli.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				return nil, obj.Err
			}
			if !strings.HasSuffix(obj.Key, ".tar.gz") {
				continue
			}
			o := Object{Key: obj.Key, Date: date, Hour: parseHour(obj.Key), Size: obj.Size}
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

// GetObject mở stream đọc object (cần Close sau khi dùng).
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	st, err := c.cli.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	obj, err := c.cli.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	return obj, st.Size, nil
}

func (c *Client) Bucket() string { return c.bucket }

func parseHour(key string) int {
	base := key
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if m := hourRe.FindStringSubmatch(base); m != nil {
		h := 0
		for _, ch := range m[1] {
			h = h*10 + int(ch-'0')
		}
		if h >= 0 && h <= 23 {
			return h
		}
	}
	return -1
}

func datesBetween(from, to string) ([]string, error) {
	t0, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, fmt.Errorf("ngày 'từ' không hợp lệ (cần yyyy-mm-dd): %v", err)
	}
	t1, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, fmt.Errorf("ngày 'đến' không hợp lệ (cần yyyy-mm-dd): %v", err)
	}
	if t1.Before(t0) {
		return nil, fmt.Errorf("khoảng ngày không hợp lệ: 'đến' trước 'từ'")
	}
	if d := int(t1.Sub(t0).Hours()/24) + 1; d > 400 {
		return nil, fmt.Errorf("khoảng quá dài (%d ngày), tối đa 400 ngày", d)
	}
	out := []string{}
	for t := t0; !t.After(t1); t = t.AddDate(0, 0, 1) {
		out = append(out, t.Format("2006-01-02"))
	}
	return out, nil
}
