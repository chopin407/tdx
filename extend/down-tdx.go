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

// DownloadTdxHsjDay 下载沪深京日线数据完整包到指定目录
// dir: 输出目录(按项目约定放 ./output/hsjday/)
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

	filename := filepath.Join(dir, "hsjday.zip")
	resp, err := http.Get(info.Url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败, http code: %d", resp.StatusCode)
	}

	f, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	// 保存更新信息,便于后续判断是否需要重新下载
	infoFile := filepath.Join(dir, "hsjday.txt")
	if err := os.WriteFile(infoFile, []byte(fmt.Sprintf(
		"更新日期: %s\n文件大小: %s\n下载地址: %s\n",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url,
	)), 0o644); err != nil {
		return "", err
	}

	logs.Infof("下载完成: %s\n", filename)
	return filename, nil
}
