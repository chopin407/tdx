package extend

import (
	"os"
	"path/filepath"
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

	// 3. 有 zip 和信息文件 → 正确解析时间
	os.WriteFile(filepath.Join(dir, hsjdayInfoFile), []byte(
		"更新日期: 2026-09-17 15:59:01\n文件大小: 524.45MB\n",
	), 0o644)
	got, err = readLocalUpdateTime(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 17, 15, 59, 1, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("更新日期: got %s, want %s", got.Format(time.DateTime), want.Format(time.DateTime))
	}
}

func TestUnzipHsjDayMissingFile(t *testing.T) {
	dir := t.TempDir()
	err := UnzipHsjDay(filepath.Join(dir, "nonexist.zip"), dir)
	if err == nil {
		t.Fatal("期望 zip 不存在报错,但返回 nil")
	}
	t.Log(err)
}
