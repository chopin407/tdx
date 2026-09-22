package extend

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// _hsjdayinfo.js 实际返回内容示例
const hsjdayJS = `/*modified at 2026-09-17 16:20:01*/
window.HSJDAY_SOFT_SIZE="524.45MB";
window.HSJDAY_SOFT_TIME="2026-09-17 15:59:01";
`

func TestParseTdxHsjDay(t *testing.T) {
	info, err := parseTdxHsjDay(hsjdayJS)
	if err != nil {
		t.Fatal(err)
	}
	// 验证更新日期
	wantTime := "2026-09-17 15:59:01"
	gotTime := info.UpdateTime.Format("2006-01-02 15:04:05")
	if gotTime != wantTime {
		t.Errorf("更新日期: got %s, want %s", gotTime, wantTime)
	}
	// 验证文件大小
	if info.Size != "524.45MB" {
		t.Errorf("文件大小: got %s, want 524.45MB", info.Size)
	}
	// 验证下载地址
	wantURL := "https://data.tdx.com.cn/vipdoc/hsjday.zip"
	if info.Url != wantURL {
		t.Errorf("下载地址: got %s, want %s", info.Url, wantURL)
	}
	t.Logf("更新日期: %s, 大小: %s, 下载地址: %s", gotTime, info.Size, info.Url)
}

func TestParseTdxHsjDayNotFound(t *testing.T) {
	// 缺少 HSJDAY_SOFT_TIME 的 JS 应报错
	_, err := parseTdxHsjDay(`window.HSJDAY_SOFT_SIZE="1MB";`)
	if err == nil {
		t.Fatal("期望报错,但返回 nil")
	}
	t.Log(err)
}

func TestGetTdxHsjDayPackage(t *testing.T) {
	info, err := GetTdxHsjDayPackage()
	if err != nil {
		t.Skipf("网络获取失败,跳过: %v", err)
		return
	}
	t.Logf("更新日期: %s, 大小: %s, 下载地址: %s",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url)
}

func TestReadLocalUpdateTime(t *testing.T) {
	dir := t.TempDir()

	// 1. 无任何文件 → 零值
	got, err := readLocalUpdateTime(dir)
	if err != nil || !got.IsZero() {
		t.Fatalf("无文件时期望零值, got=%v err=%v", got, err)
	}

	// 2. 只有 zip 没有信息文件 → 零值(需下载)
	os.WriteFile(filepath.Join(dir, hsjdayZipFile), []byte("x"), 0o644)
	got, err = readLocalUpdateTime(dir)
	if err != nil || !got.IsZero() {
		t.Fatalf("缺信息文件时期望零值, got=%v err=%v", got, err)
	}

	// 3. 损坏的 zip 即使有信息文件也必须重新下载
	os.WriteFile(filepath.Join(dir, hsjdayInfoFile), []byte(
		"更新日期: 2026-09-17 15:59:01\n文件大小: 524.45MB\n",
	), 0o644)
	got, err = readLocalUpdateTime(dir)
	if err != nil || !got.IsZero() {
		t.Fatalf("损坏 zip 期望零值, got=%v err=%v", got, err)
	}

	// 4. 有效 zip 和信息文件 → 正确解析时间
	if err := os.WriteFile(filepath.Join(dir, hsjdayZipFile), testZip(t), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = readLocalUpdateTime(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 17, 15, 59, 1, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("更新日期: got %s, want %s", got.Format(time.DateTime), want.Format(time.DateTime))
	}
}

func TestExpectedPackageBytes(t *testing.T) {
	if got, want := expectedPackageBytes("524.79MB"), int64(550282199); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	if got := expectedPackageBytes("unknown"); got != 0 {
		t.Fatalf("unknown size should be zero, got %d", got)
	}
}

func TestDownloadTdxHsjDayPackageRejectsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>rate limited</html>"))
	}))
	defer server.Close()

	dir := t.TempDir()
	_, err := downloadTdxHsjDayPackage(&TdxHsjDayPackage{
		UpdateTime: time.Now(),
		Size:       "524.79MB",
		Url:        server.URL,
	}, dir)
	if err == nil || !strings.Contains(err.Error(), "响应体过小") {
		t.Fatalf("expected too-small error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, hsjdayPartFile)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid partial file was not removed: %v", statErr)
	}
}

func TestDownloadTdxHsjDayPackageValidZip(t *testing.T) {
	body := testZip(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != UrlTdxVipData || r.Header.Get("User-Agent") == "" {
			t.Errorf("missing download headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	dir := t.TempDir()
	path, err := downloadTdxHsjDayPackage(&TdxHsjDayPackage{
		UpdateTime: time.Date(2026, 9, 21, 15, 56, 17, 0, time.Local),
		Url:        server.URL,
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := validZipArchive(path); err != nil {
		t.Fatalf("downloaded zip invalid: %v", err)
	}
}

func testZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("vipdoc/sh/lday/sh000001.day")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnzipHsjDayMissingFile(t *testing.T) {
	dir := t.TempDir()
	err := UnzipHsjDay(filepath.Join(dir, "nonexist.zip"), dir)
	if err == nil {
		t.Fatal("期望 zip 不存在报错,但返回 nil")
	}
	t.Log(err)
}

func TestUnzipHsjDayNormalizesWindowsPathsIntoVipdoc(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "hsjday.zip")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(`vipdoc\sh\lday\sh000001.day`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("day-data")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	vipdoc := filepath.Join(dir, "vipdoc")
	if err := UnzipHsjDay(zipPath, vipdoc); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(vipdoc, "sh", "lday", "sh000001.day"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "day-data" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, `sh\lday\sh000001.day`)); !os.IsNotExist(err) {
		t.Fatalf("file escaped configured vipdoc: %v", err)
	}
}

func TestUnzipHsjDayRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bad.zip")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if _, err := w.Create(`vipdoc\..\outside.day`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UnzipHsjDay(zipPath, filepath.Join(dir, "vipdoc")); err == nil {
		t.Fatal("expected traversal error")
	}
}
