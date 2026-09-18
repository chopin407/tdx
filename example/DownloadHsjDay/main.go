package main

import (
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/extend"
)

// 下载通达信沪深京日线数据完整包到 ./output/hsjday/
// 数据来源: https://www.tdx.com.cn/article/vipdata.html
func main() {
	// 仅获取最新信息(更新日期+文件大小+下载地址)
	info, err := extend.GetTdxHsjDayPackage()
	if err != nil {
		logs.Err(err)
		return
	}
	logs.Infof("更新日期: %s 大小: %s 下载地址: %s\n",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url)

	// 下载完整包到 ./output/hsjday/hsjday.zip
	filename, err := extend.DownloadTdxHsjDay("./output/hsjday/")
	if err != nil {
		logs.Err(err)
		return
	}
	logs.Infof("下载完成: %s\n", filename)
}
