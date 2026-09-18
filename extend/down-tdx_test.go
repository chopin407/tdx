package extend

import (
	"testing"
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
