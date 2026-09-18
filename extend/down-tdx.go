package extend

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/lib/zip"
)

const (
	// UrlTdxVipData 通达信盘后数据下载页面
	UrlTdxVipData = "https://www.tdx.com.cn/article/vipdata.html"
	// UrlTdxHsjDayInfo 沪深京日线数据完整包的更新信息(由 vipdata.html 页面动态加载)
	UrlTdxHsjDayInfo = "https://data.tdx.com.cn/vipdoc/_hsjdayinfo.js"
	// UrlTdxHsjDayZip 沪深京日线数据完整包下载地址
	UrlTdxHsjDayZip = "https://data.tdx.com.cn/vipdoc/hsjday.zip"
	// TdxHsjDayName 沪深京日线数据完整包名称
	TdxHsjDayName = "沪深京日线数据完整包"

	hsjdayZipFile  = "hsjday.zip"
	hsjdayPartFile = "hsjday.zip.part"
	hsjdayInfoFile = "hsjday.txt"
)

// TdxHsjDayPackage 沪深京日线数据完整包信息
type TdxHsjDayPackage struct {
	UpdateTime time.Time // 更新日期
	Size       string    // 文件大小(如 "524.45MB")
	Url        string    // 下载地址
}

// GetTdxHsjDayPackage 获取沪深京日线数据完整包的最新信息(更新日期+文件大小+下载地址)
// 数据来源: https://data.tdx.com.cn/vipdoc/_hsjdayinfo.js (vipdata.html 页面动态加载)
func GetTdxHsjDayPackage() (*TdxHsjDayPackage, error) {
	resp, err := http.Get(UrlTdxHsjDayInfo)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取信息失败, http code: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseTdxHsjDay(string(body))
}

var (
	// reHsjDayTime 匹配 window.HSJDAY_SOFT_TIME="YYYY-MM-DD HH:MM:SS"
	reHsjDayTime = regexp.MustCompile(`HSJDAY_SOFT_TIME\s*=\s*"([^"]+)"`)
	// reHsjDaySize 匹配 window.HSJDAY_SOFT_SIZE="524.45MB"
	reHsjDaySize = regexp.MustCompile(`HSJDAY_SOFT_SIZE\s*=\s*"([^"]+)"`)
	// reLocalUpdateTime 从本地信息文件中解析更新日期,匹配 "更新日期: 2026-09-17 15:59:01"
	reLocalUpdateTime = regexp.MustCompile(`更新日期:\s*(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
)

// parseTdxHsjDay 从 _hsjdayinfo.js 内容中解析沪深京日线数据完整包信息
func parseTdxHsjDay(js string) (*TdxHsjDayPackage, error) {
	m := reHsjDayTime.FindStringSubmatch(js)
	if len(m) < 2 {
		return nil, fmt.Errorf("未找到沪深京日线数据完整包更新日期")
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(m[1]), time.Local)
	if err != nil {
		return nil, fmt.Errorf("解析更新日期失败: %w", err)
	}
	size := ""
	if m2 := reHsjDaySize.FindStringSubmatch(js); len(m2) >= 2 {
		size = m2[1]
	}
	return &TdxHsjDayPackage{
		UpdateTime: t,
		Size:       size,
		Url:        UrlTdxHsjDayZip,
	}, nil
}

// readLocalUpdateTime 读取本地已下载文件的更新日期
// 若本地信息文件不存在或 zip 缺失,返回零值 time 与 nil error(表示需要下载)
func readLocalUpdateTime(dir string) (time.Time, error) {
	infoPath := filepath.Join(dir, hsjdayInfoFile)
	zipPath := filepath.Join(dir, hsjdayZipFile)
	if !exists(zipPath) || !exists(infoPath) {
		return time.Time{}, nil
	}
	bs, err := os.ReadFile(infoPath)
	if err != nil {
		return time.Time{}, nil
	}
	m := reLocalUpdateTime.FindStringSubmatch(string(bs))
	if len(m) < 2 {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local)
	if err != nil {
		return time.Time{}, nil
	}
	return t, nil
}

// DownloadTdxHsjDay 下载沪深京日线数据完整包到指定目录
// dir: 输出目录(按项目约定放 ./output/hsjday/)
// 若本地已为最新版本(更新日期与服务器一致且 zip 存在),则跳过下载直接返回本地路径。
// 下载采用原子写入:先写入 .part,完成后再重命名为 hsjday.zip,避免半成品被误用。
// 返回下载的 zip 文件路径
func DownloadTdxHsjDay(dir string) (string, error) {
	info, err := GetTdxHsjDayPackage()
	if err != nil {
		return "", err
	}
	logs.Infof("沪深京日线数据完整包 更新日期: %s 大小: %s 下载地址: %s\n",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	zipPath := filepath.Join(dir, hsjdayZipFile)

	// 校验:本地已是最新则跳过下载
	localTime, err := readLocalUpdateTime(dir)
	if err == nil && !localTime.IsZero() && !localTime.Before(info.UpdateTime) {
		logs.Infof("本地已是最新(更新日期: %s),跳过下载: %s\n",
			localTime.Format("2006-01-02 15:04:05"), zipPath)
		return zipPath, nil
	}

	resp, err := http.Get(info.Url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败, http code: %d", resp.StatusCode)
	}

	// 原子写入:先写 .part 临时文件,成功后重命名,避免半成品被当作已完成
	partPath := filepath.Join(dir, hsjdayPartFile)
	f, err := os.Create(partPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(partPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(partPath)
		return "", err
	}
	if err := os.Rename(partPath, zipPath); err != nil {
		os.Remove(partPath)
		return "", err
	}

	// 保存更新信息,便于后续判断是否需要重新下载
	infoPath := filepath.Join(dir, hsjdayInfoFile)
	if err := os.WriteFile(infoPath, []byte(fmt.Sprintf(
		"更新日期: %s\n文件大小: %s\n下载地址: %s\n",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url,
	)), 0o644); err != nil {
		return "", err
	}

	logs.Infof("下载完成: %s\n", zipPath)
	return zipPath, nil
}

// UnzipHsjDay 解压沪深京日线数据完整包到指定数据目录
// zipPath: 已下载的 hsjday.zip 路径
// dataDir: 解压目标目录(如 ./data/vipdoc),zip 内含 vipdoc/<sh|sz|bj>/lday/*.day
func UnzipHsjDay(zipPath, dataDir string) error {
	if !exists(zipPath) {
		return fmt.Errorf("zip 文件不存在: %s", zipPath)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if err := zip.Decode(zipPath, dataDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	logs.Infof("解压完成: %s -> %s\n", zipPath, dataDir)
	return nil
}

// DownloadAndUnzipHsjDay 下载并解压沪深京日线数据完整包
// downloadDir: zip 下载目录(如 ./output/hsjday/)
// dataDir: 解压目标目录(如 ./data)
// 返回下载的 zip 文件路径
func DownloadAndUnzipHsjDay(downloadDir, dataDir string) (string, error) {
	zipPath, err := DownloadTdxHsjDay(downloadDir)
	if err != nil {
		return "", err
	}
	if err := UnzipHsjDay(zipPath, dataDir); err != nil {
		return zipPath, err
	}
	return zipPath, nil
}
