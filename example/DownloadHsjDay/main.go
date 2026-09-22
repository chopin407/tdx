package main

import (
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/extend"
)

// 下载通达信沪深京日线数据完整包到 ./output/hsjday/ 并解压到 ./data/vipdoc
// 数据来源: https://www.tdx.com.cn/article/vipdata.html
func main() {
	// 1. 仅获取最新信息(更新日期+文件大小+下载地址)
	info, err := extend.GetTdxHsjDayPackage()
	if err != nil {
		logs.Err(err)
		return
	}
	logs.Infof("更新日期: %s 大小: %s 下载地址: %s\n",
		info.UpdateTime.Format("2006-01-02 15:04:05"), info.Size, info.Url)

	// 2. 下载并解压:下载到 ./output/hsjday/,解压到 ./data/vipdoc
	//    若本地已是最新版本会自动跳过下载
	zipPath, err := extend.DownloadAndUnzipHsjDay("./output/hsjday/", "./data/vipdoc")
	if err != nil {
		logs.Err(err)
		return
	}
	logs.Infof("处理完成: %s\n", zipPath)
}
