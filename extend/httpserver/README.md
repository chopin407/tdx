# tdx HTTP Server

将通达信(tdx)行情数据通过 HTTP API 对外开放。本包基于 `tdx.Client` 实现,以 RESTful GET 接口暴露股票、指数、扩展行情等数据。

## 快速开始

```go
package main

import (
	"log"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend/httpserver"
)

func main() {
	// 方式一: 默认配置(开启断线重连)
	s, err := httpserver.Default()
	if err != nil {
		log.Fatal(err)
	}

	// 方式二: 自定义配置
	s, err = httpserver.New(
		httpserver.WithAddr(":8080"),
		httpserver.WithPoolSize(2),
		httpserver.WithExHqHosts(tdx.ExHosts...), // 可选,启用扩展行情 /ex/* 路由
		httpserver.WithOptions(tdx.WithRedial()),
	)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("服务启动,监听 :8080")
	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}
```

## 配置选项

使用函数式选项(Functional Options)配置服务:

| 选项函数 | 说明 | 默认值 |
| --- | --- | --- |
| `WithAddr(addr)` | HTTP 监听地址 | `":8080"` |
| `WithHosts(hosts...)` | 标准行情服务器列表 | `tdx.Hosts` |
| `WithPoolSize(n)` | 标准连接池大小 | `1` |
| `WithExHqHosts(hosts...)` | 扩展行情服务器列表,为空则不启用扩展行情 | 无 |
| `WithExPoolSize(n)` | 扩展连接池大小 | `1` |
| `WithOptions(opts...)` | 通达信连接选项,如 `tdx.WithDebug()`、`tdx.WithRedial()` | 无 |
| `WithCodesOptions(opts...)` | 证券元数据缓存配置，例如自定义缓存数据库路径 | 无 |

`Server.Handler()` 可将原有行情路由嵌入其他 HTTP 服务。完整的 DuckDB 研究入口见 [研究服务](../../../docs/research.md)。

> `Default()` 会自动添加 `tdx.WithRedial()` 断线重连选项。

## 响应格式

所有接口统一返回如下 JSON 结构:

**成功:**

```json
{
  "code": 0,
  "msg": "ok",
  "data": { ... }
}
```

**错误:**

```json
{
  "code": 1,
  "msg": "错误信息",
  "data": null
}
```

## API 路由

### 健康检查

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /` | 无 | 健康检查,返回服务状态 |

### 代码/数量

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /count` | `exchange` | 获取指定交易所的证券数量 |
| `GET /code` | `exchange`, `start` | 获取指定交易所的证券代码(分页) |
| `GET /code/all` | `exchange` | 获取指定交易所的全部证券代码 |
| `GET /code/stocks` | 无 | 获取全部股票代码 |
| `GET /code/etfs` | 无 | 获取全部 ETF 代码 |
| `GET /code/indexes` | 无 | 获取全部指数代码 |

### 行情/财务

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /quote` | `codes` | 获取实时行情报价(支持多个代码) |
| `GET /call_auction` | `code` | 获取集合竞价数据 |
| `GET /gbbq` | `code` | 获取除权除息(股本变更)数据 |
| `GET /finance` | `exchange`, `code` | 获取财务信息 |
| `GET /company/category` | `exchange`, `code` | 获取公司信息(F10)文件目录 |
| `GET /company/content` | `exchange`, `code`, `filename`, `start`, `length` | 获取公司信息(F10)文件内容 |

### 分时/成交

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /minute` | `code` | 获取当日分时数据 |
| `GET /minute/history` | `date`, `code` | 获取历史分时数据 |
| `GET /trade` | `code`, `start`, `count` | 获取当日分笔成交明细(分页) |
| `GET /trade/all` | `code` | 获取当日全部分笔成交明细 |
| `GET /trade/history` | `date`, `code`, `start`, `count` | 获取历史分笔成交明细(分页) |
| `GET /trade/history/day` | `date`, `code` | 获取指定日期全部分笔成交明细 |

### K线(股票)

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /kline` | `type`, `code`, `start`, `count` | 获取指定类型的 K 线(分页) |
| `GET /kline/all` | `type`, `code` | 获取指定类型的全部 K 线 |
| `GET /kline/minute` | `code`, `start`, `count` | 获取 1 分钟 K 线(分页) |
| `GET /kline/minute/all` | `code` | 获取全部 1 分钟 K 线 |
| `GET /kline/5minute` | `code`, `start`, `count` | 获取 5 分钟 K 线(分页) |
| `GET /kline/5minute/all` | `code` | 获取全部 5 分钟 K 线 |
| `GET /kline/15minute` | `code`, `start`, `count` | 获取 15 分钟 K 线(分页) |
| `GET /kline/15minute/all` | `code` | 获取全部 15 分钟 K 线 |
| `GET /kline/30minute` | `code`, `start`, `count` | 获取 30 分钟 K 线(分页) |
| `GET /kline/30minute/all` | `code` | 获取全部 30 分钟 K 线 |
| `GET /kline/60minute` | `code`, `start`, `count` | 获取 60 分钟 K 线(分页) |
| `GET /kline/60minute/all` | `code` | 获取全部 60 分钟 K 线 |
| `GET /kline/day` | `code`, `start`, `count` | 获取日 K 线(分页) |
| `GET /kline/day/all` | `code` | 获取全部日 K 线 |
| `GET /kline/week` | `code`, `start`, `count` | 获取周 K 线(分页) |
| `GET /kline/week/all` | `code` | 获取全部周 K 线 |
| `GET /kline/month` | `code`, `start`, `count` | 获取月 K 线(分页) |
| `GET /kline/month/all` | `code` | 获取全部月 K 线 |
| `GET /kline/quarter` | `code`, `start`, `count` | 获取季 K 线(分页) |
| `GET /kline/quarter/all` | `code` | 获取全部季 K 线 |
| `GET /kline/year` | `code`, `start`, `count` | 获取年 K 线(分页) |
| `GET /kline/year/all` | `code` | 获取全部年 K 线 |

### 指数K线

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /index` | `type`, `code`, `start`, `count` | 获取指定类型的指数 K 线(分页) |
| `GET /index/all` | `type`, `code` | 获取指定类型的全部指数 K 线 |
| `GET /index/minute` | `code`, `start`, `count` | 获取指数 1 分钟 K 线(分页) |
| `GET /index/5minute` | `code`, `start`, `count` | 获取指数 5 分钟 K 线(分页) |
| `GET /index/15minute` | `code`, `start`, `count` | 获取指数 15 分钟 K 线(分页) |
| `GET /index/30minute` | `code`, `start`, `count` | 获取指数 30 分钟 K 线(分页) |
| `GET /index/60minute` | `code`, `start`, `count` | 获取指数 60 分钟 K 线(分页) |
| `GET /index/day` | `code`, `start`, `count` | 获取指数日 K 线(分页) |
| `GET /index/day/all` | `code` | 获取全部指数日 K 线 |
| `GET /index/week/all` | `code` | 获取全部指数周 K 线 |
| `GET /index/month/all` | `code` | 获取全部指数月 K 线 |
| `GET /index/quarter/all` | `code` | 获取全部指数季 K 线 |
| `GET /index/year/all` | `code` | 获取全部指数年 K 线 |

### 板块/报表

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /block/data` | `file` | 获取板块数据(解析后) |
| `GET /block/data/index` | `file` | 获取带索引的板块数据 |
| `GET /block/file` | `file` | 获取板块原始文件内容 |
| `GET /report/file` | `file` | 获取报表文件内容 |
| `GET /zhb/files` | 无 | 获取 ZHB 文件列表 |
| `GET /tdx/zs` | 无 | 获取通达信指数信息 |
| `GET /tdx/bk` | 无 | 获取通达信板块信息 |
| `GET /tdx/stat` | 无 | 获取通达信统计信息 |
| `GET /tdx/stat2` | 无 | 获取通达信统计信息(二) |
| `GET /tdx/xgsg` | 无 | 获取新股申购信息 |
| `GET /tdx/hy` | 无 | 获取通达信行业信息 |
| `GET /spblock` | 无 | 获取特殊板块信息 |

### 扩展行情

> 需要通过 `WithExHqHosts()` 选项启用,否则返回 404。

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /ex/markets` | 无 | 获取扩展行情市场列表 |
| `GET /ex/count` | 无 | 获取扩展行情证券数量 |
| `GET /ex/instruments` | `start`, `count` | 获取扩展行情证券列表(分页) |
| `GET /ex/quote` | `market`, `code` | 获取扩展行情实时报价 |
| `GET /ex/quote_list` | `market`, `category`, `start`, `count` | 获取扩展行情报价列表(分页) |
| `GET /ex/bars` | `category`, `market`, `code`, `start`, `count` | 获取扩展行情 K 线(分页) |
| `GET /ex/minute` | `market`, `code` | 获取扩展行情分时数据 |
| `GET /ex/minute/hist` | `market`, `code`, `date` | 获取扩展行情历史分时数据 |
| `GET /ex/trade` | `market`, `code`, `start`, `count` | 获取扩展行情分笔成交(分页) |
| `GET /ex/trade/hist` | `market`, `code`, `date`, `start`, `count` | 获取扩展行情历史分笔成交(分页) |
| `GET /ex/bars/range` | `market`, `code`, `date`, `date2` | 获取扩展行情指定日期区间 K 线 |

## 参数说明

| 参数 | 说明 | 示例 |
| --- | --- | --- |
| `exchange` | 交易所代码,可选 `sh`(上海)、`sz`(深圳)、`bj`(北京) | `sh` |
| `code` | 证券代码,可带交易所前缀 | `600519` 或 `sh600519` |
| `codes` | 多个证券代码,逗号分隔 | `sz000001,sh600008` |
| `type` | K 线类型(数字),见下表 | `9` |
| `start` | 起始位置(数字) | `0` |
| `count` | 获取数量(数字) | `100` |
| `date` | 日期,格式 `YYYYMMDD`(如 `20240101`) | `20240101` |
| `market` | 扩展行情市场代码(数字) | `47` |
| `category` | 扩展行情类别(数字) | `1` |
| `file` | 板块/报表文件名 | `block_gn.dat` |
| `filename` | F10 公司信息文件名 | `300052.txt` |
| `length` | 长度(数字) | `5000` |
| `date2` | 结束日期,格式 `YYYYMMDD` | `20240601` |

**K 线类型(`type`)对照表:**

| 值 | 说明 |
| --- | --- |
| `0` | 5 分钟 |
| `1` | 15 分钟 |
| `2` | 30 分钟 |
| `3` | 60 分钟 |
| `4` | 日K(变体,数值需除以 100) |
| `5` | 周 |
| `6` | 月 |
| `7` | 1 分钟 |
| `8` | 1 分钟(变体) |
| `9` | 日 |
| `10` | 季 |
| `11` | 年 |

## 使用示例

**获取报价:**

```bash
curl "http://localhost:8080/quote?codes=sz000001,sh600519"
```

**获取日 K 线:**

```bash
# 使用通用接口,指定 type=9(日)
curl "http://localhost:8080/kline?type=9&code=600519&start=0&count=100"

# 使用专用接口
curl "http://localhost:8080/kline/day?code=600519&start=0&count=100"
```

**获取扩展行情:**

```bash
# 获取扩展行情市场列表
curl "http://localhost:8080/ex/markets"

# 获取扩展行情报价
curl "http://localhost:8080/ex/quote?market=47&code=600519"
```

## 前后复权日线与补齐接口

新增日线接口复用 protocol 的仿射复权算法（现金分红包含加法偏移，不能只乘一个比例因子），不依赖 DuckDB 或本地股本缓存。

| 路径 | 参数 | 含义 |
| --- | --- | --- |
| `/kline/day/qfq` | `code,start,count` | 前复权日线分页 |
| `/kline/day/qfq/all` | `code` | 前复权全部可获取日线 |
| `/kline/day/hfq` | `code,start,count` | 后复权日线分页 |
| `/kline/day/hfq/all` | `code` | 后复权全部可获取日线 |
| `/kline/day/factors` | `code` | 各日仿射复权因子 |
| `/index/minute/all`、`/index/5minute/all`、`/index/15minute/all`、`/index/30minute/all`、`/index/60minute/all` | `code` | 补齐指数分钟全量路由 |
| `/index/week`、`/index/month`、`/index/quarter`、`/index/year` | `code,start,count` | 补齐指数长周期分页路由 |

原 `/kline/day` 和 `/kline/day/all` 同时支持 `adjust=none|qfq|hfq`，省略时保持原不复权行为。其他周期接口仍为不复权，不支持该参数。

```bash
curl "http://192.168.1.74:8080/kline/day/qfq?code=sz000001&start=0&count=100"
curl "http://192.168.1.74:8080/kline/day/hfq/all?code=sh600519"
curl "http://192.168.1.74:8080/kline/day/all?code=sh600519&adjust=qfq"
curl "http://192.168.1.74:8080/kline/day/factors?code=sz000001"
```

如果使用 tdx-research 入口，以上请求也需要其 Bearer Token。

- 分页 `start=0` 表示最新一段，`count` 为 1..800；返回 `data.Count` 和按时间升序的 `data.List`，越界返回空列表。
- 先拉取服务器可提供的全部日线和股本变迁，再复权、分页，因此复权分页请求成本高于普通分页；批量研究应使用 DuckDB 研究服务。
- 前复权锚定最新可获取交易日，后复权锚定最早可获取交易日，并非保证上市首日。上游可获取历史变化时，后复权基准也可能变化。
- OHLC 和昨收沿用已有算法四舍五入到分，成交量、成交额不复权；不适合要求厘级精度的基金复权。价格 JSON 单位沿用原协议类型。
- 因子字段 `QFQMul/QFQAdd`、`HFQMul/HFQAdd` 满足“复权元价 = Mul × 原始元价 + Add”。旧 `QFQ/HFQ` 比例字段不能完整表达现金分红。
- 晚于最新日线的除权事件不参与计算；扩缩股（11/12 类）明确报错。该实时接口不提供历史时点快照，回测请使用研究服务。
- 新复权接口参数错误 HTTP 400，上游/计算失败 HTTP 502；不会将失败伪装成不复权成功。未注册路径现在正确返回 404，健康检查仅匹配 `/`。

## 功能表覆盖核对（2026-09-18）

README 功能表中的行情、证券列表、分时/成交、K 线/指数、集合竞价、财务、F10、板块、行业、报表、统计、新股和扩展行情均已有对应 HTTP 数据接口。此次补齐：

| 路径 | 参数 | 说明 |
| --- | --- | --- |
| `GET /gbbq/all` | 可选 `codes` | 不传时查询当前全市场股票的股本变迁，对应 `GetGbbqAll` 能力；传入时批量查询最多 100 个沪深北证券代码，自动去重 |
| `GET /kline/hour` | `code,start,count` | `/kline/60minute` 别名，对应 `GetKlineHour` |
| `GET /kline/hour/all` | `code` | `/kline/60minute/all` 别名，对应 `GetKlineHourAll` |

```bash
curl "http://192.168.1.74:8080/gbbq/all?codes=sz000001,sh600519"
```

`/gbbq/all` 返回 `data` 为证券代码到股本记录数组的映射，无记录时为 `[]`。非法批量参数返回 HTTP 400；任一上游请求失败返回 HTTP 502 和 `code=1`，不返回貌似完整的部分数据。全市场查询耗时较长，每只证券后释放连接池供其他请求使用；客户端取消后在下一次请求前停止（在途协议请求仍受底层超时控制）。推荐按代码分批调用。全市场范围采用当前股票列表，不包含已经退市且不在列表中的证券。

覆盖边界：Go 的 `*Until` 回调方法、`GetKlineMinute241Until` 补点处理，以及依赖 `Workday` 的 `GetHistoryTradeFull/Before` 跨日遍历尚未直接映射 HTTP；逐日成交与通用周期接口已提供，复杂历史遍历应由研究任务编排。`DialExHq` 是连接配置，通过 `WithExHqHosts` 启用，而非 HTTP 数据路由。复权公开的是日线查询，`QFQ/HFQ` 对调用方自带数组的本地计算不另设上传接口。
