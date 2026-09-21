package extend

import (
	archivezip "archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/injoyai/logs"
	ziputil "github.com/injoyai/tdx/lib/zip"
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
	req, err := http.NewRequest(http.MethodGet, UrlTdxHsjDayInfo, nil)
	if err != nil {
		return nil, err
	}
	setTdxDownloadHeaders(req)
	resp, err := http.DefaultClient.Do(req)
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

func setTdxDownloadHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/128 Safari/537.36")
	req.Header.Set("Referer", UrlTdxVipData)
	req.Header.Set("Accept", "application/zip,application/octet-stream,*/*")
}

func validZipArchive(path string) error {
	r, err := archivezip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) == 0 {
		return fmt.Errorf("zip archive contains no files")
	}
	return nil
}

func expectedPackageBytes(size string) int64 {
	m := regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(KB|MB|GB)\s*$`).FindStringSubmatch(size)
	if len(m) != 3 {
		return 0
	}
	var value float64
	if _, err := fmt.Sscanf(m[1], "%f", &value); err != nil {
		return 0
	}
	multiplier := float64(1 << 10)
	switch strings.ToUpper(m[2]) {
	case "MB":
		multiplier = 1 << 20
	case "GB":
		multiplier = 1 << 30
	}
	return int64(value * multiplier)
}

func downloadWithCurl(url, path string) (int64, error) {
	if _, err := exec.LookPath("curl"); err != nil {
		return 0, fmt.Errorf("curl 不可用: %w", err)
	}
	cmd := exec.Command("curl",
		"--silent", "--show-error", "--fail", "--location",
		"--retry", "2", "--retry-delay", "2", "--connect-timeout", "30",
		"--user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/128 Safari/537.36",
		"--referer", UrlTdxVipData,
		"--output", path, url,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return 0, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return stat.Size(), nil
}

func validateDownloadedPackage(path, size string, written int64) error {
	if expected := expectedPackageBytes(size); expected > 0 && written < expected*9/10 {
		return fmt.Errorf("响应体过小: 实际 %d 字节, 预期约 %d 字节", written, expected)
	}
	if err := validZipArchive(path); err != nil {
		return fmt.Errorf("不是有效 ZIP: %w", err)
	}
	return nil
}

func filePrefix(path string, limit int) string {
	preview := make([]byte, limit)
	source, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer source.Close()
	n, _ := source.Read(preview)
	return string(preview[:n])
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
	if err := validZipArchive(zipPath); err != nil {
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

	return downloadTdxHsjDayPackage(info, dir)
}

func downloadTdxHsjDayPackage(info *TdxHsjDayPackage, dir string) (string, error) {
	zipPath := filepath.Join(dir, hsjdayZipFile)
	req, err := http.NewRequest(http.MethodGet, info.Url, nil)
	if err != nil {
		return "", err
	}
	setTdxDownloadHeaders(req)
	resp, err := http.DefaultClient.Do(req)
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
	written, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(partPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(partPath)
		return "", err
	}
	if validationErr := validateDownloadedPackage(partPath, info.Size, written); validationErr != nil {
		goPrefix := filePrefix(partPath, 160)
		os.Remove(partPath)
		if info.Url != UrlTdxHsjDayZip {
			return "", fmt.Errorf("下载内容校验失败 (%s, content-type=%q, prefix=%q): %w", info.Url, resp.Header.Get("Content-Type"), goPrefix, validationErr)
		}
		logs.Infof("Go HTTP 收到无效内容，改用系统 curl 重试: %v\n", validationErr)
		curlWritten, curlErr := downloadWithCurl(info.Url, partPath)
		if curlErr != nil {
			return "", fmt.Errorf("Go HTTP 响应无效 (content-type=%q, prefix=%q)，curl 重试失败: %w", resp.Header.Get("Content-Type"), goPrefix, curlErr)
		}
		if curlValidationErr := validateDownloadedPackage(partPath, info.Size, curlWritten); curlValidationErr != nil {
			curlPrefix := filePrefix(partPath, 160)
			os.Remove(partPath)
			return "", fmt.Errorf("Go HTTP 与 curl 均收到无效内容; curl prefix=%q: %w", curlPrefix, curlValidationErr)
		}
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
	if err := validZipArchive(zipPath); err != nil {
		return fmt.Errorf("zip 校验失败: %w", err)
	}
	if err := ziputil.Decode(zipPath, dataDir); err != nil {
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
