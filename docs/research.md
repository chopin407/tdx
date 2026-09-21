# 个人研究闭环（DuckDB，日线 v1）

## 方案 A：MTF-A 多周期筛选与次日执行

当前实现把《交易体系与复盘框架总手册》的 MTF-A v1.0 固化为同一套可复现规则：月线以最近完整日历月的收盘、6月/12月均线输出上升/横盘/下降背景标签（不参与 v1.0 硬过滤），只用已完成周线判断 `SMA10W/SMA20W` 趋势，日线要求站上且保持上升的 `SMA20`，识别 3–8 日缩量整理、突破前一日高点及收盘位置；结构低点减 `0.3×ATR14` 为止损，历史局部压力为目标，通过 `Pmax=(T+2S-3c)/3` 限定最高买价，并强制止损距离不超过 5%、计划盈亏比不低于 2。

硬过滤默认排除名称含 ST/退及证券档案中的 ST 标记，并要求 20 日平均成交额不低于 5 亿元、当前流通市值不低于 50 亿元、至少 60 根日线。阈值可通过 `MTFAConfig` 调整。形态评分与可交易资格分开：评分高但主线、覆盖率、风险收益或硬门槛不合格的标的仍不得进入执行状态机。

次日执行状态固定为 `monitoring → order-allowed / no-chase-wait / cancelled`。计划只在下一交易日有效；市场/账户/主线许可失败、重大风险、入场前先触及结构止损会取消；价格超过 Pmax 或流动性异常只等待、不追。此接口只输出研究建议，不连接券商、不自动下单。

组合回测按信号日收盘生成计划、下一交易日日线保守撮合，默认 100 万本金、单笔风险 0.25%、最多 3 仓、单行业最多 2 仓、单仓不超过 30%，包含佣金、最低佣金、印花税和滑点；止损至少 T+1，收盘跌破前复权 MA10 后下一交易日退出。返回收益、最大回撤、胜率和平均 R。

离线目录不写死在程序中，通过环境变量 `TDX_VIPDOC_DIR` 配置，也可用命令行 `-import-dir` 显式覆盖；两者都必须是服务器上的绝对路径。Debian 建议使用 `/srv/tdx/vipdoc`。服务与原行情并行时可让研究服务使用 `:8081`：

```powershell
$env:TDX_API_TOKEN='替换为至少16位的随机令牌'
$env:TDX_VIPDOC_DIR='D:\new_tdx\vipdoc'
.\output\bin\tdx-research.exe -addr :8081 -db output\research\market.duckdb -schedule 19:00
```

首次依次执行 `import → update → daily`。`import` 只导入原始日线；`update` 补齐公司行为、当前名称/行业/流通股本并恢复复权可用状态；`daily` 在线更新后生成复盘与 MTF-A 计划。离线包本身不含这些档案字段，不能跳过在线补齐。

新增接口：`POST /v1/mtfa/screens`、`GET /v1/mtfa/latest`、`POST /v1/mtfa/execution`、`POST /v1/mtfa/backtests`、`GET /v1/data-coverage`。所有接口仍要求 Bearer token。

明确缺口：历史 ST 状态、历史行业成员、历史流通股本和完整财务公告时点快照当前不可得；因此跨历史区间组合回测会返回 `research_only=true`，使用当前证券档案并在 `data_gaps` 中标明，不能宣称无幸存者偏差。只有日线 OHLC 时也无法还原触发价与止损价同日发生的真实先后顺序，这类样本会保守阻断。节假日缩短周暂按最后交易日不是周五而视为未完成周线，后续接入正式交易日历再修正。

`cmd/tdx-research` 是新增的统一服务入口：离线历史入库 → 在线增量更新 → 策略信号 → 历史回测 → 选股和盘后复盘。原来的 `tools/httpserver.go` 入口仍可使用，但没有研究数据库功能。两个入口不要同时占用 8080。

第一版所有策略计算都在 Go 的 `extend/research` 中，共用同一信号实现，避免为了部署再维护 Python 服务。Python 可以消费 JSON 数据集和结果。没有自动下单功能。

## Debian 原生运行（推荐）

机器已安装 DuckDB CLI 也可以直接使用本服务。Go 驱动自带 DuckDB 引擎，**不调用系统 `duckdb` 可执行文件**；其引擎版本由 `go.mod` 锁定。请选择专用的新数据库路径，避免用不同版本 CLI 改写服务正在使用的文件。

需要 Go（不低于 go.mod 指定版本）、C/C++ 编译器及系统证书。Debian 构建示例：

```bash
sudo apt-get update
sudo apt-get install -y build-essential ca-certificates
cd /opt/tdx                         # 换成实际项目路径
mkdir -p output/bin output/research
CGO_ENABLED=1 go build -trimpath -o output/bin/tdx-research ./cmd/tdx-research
go build -trimpath -o output/bin/tdx-down ./cmd/tdx-down

# 生成后保存到自己的部署环境文件，不要提交到 Git。
export TDX_API_TOKEN="$(openssl rand -hex 24)"
export TDX_VIPDOC_DIR=/srv/tdx/vipdoc
./output/bin/tdx-research \
  -addr :8080 \
  -db output/research/market.duckdb \
  -schedule 19:00
```

Windows 的 `D:\new_tdx\vipdoc` 需事先复制到 Debian，例如 `/srv/tdx/vipdoc`；源目录在导入期间应保持不变。程序只读该目录，不会改写通达信文件。若只验证离线功能，加 `-offline -schedule ''`。

`-symbols sh600519,sz000001` 限定自动更新池；不设置则发现上游当前 A 股列表，包含新上市股票。已退市股票离线历史保留，但不通过当前列表自动更新。ETF/指数需通过显式 `symbols` 指定更新。默认盘中不发布日线，上海时间 16:30 后才接受当日日线；实时报价/分时仍走原来的行情接口。

首次建议 `-schedule ''` 暂停自动任务，导入验收完成后再启用。服务只接受一个导入/更新任务同时运行，冲突返回 409。离线导入期间仍可查询已提交的数据。

所有路由（包括原有报价接口）在这个新入口下要求：

```text
Authorization: Bearer <TDX_API_TOKEN>
```

令牌至少 16 字符。只在可信局域网开放；跨不可信网络使用 HTTPS 反向代理。默认不会开放原始 SQL 执行接口。

## systemd 常驻

提供了 `deploy/tdx-research.service` 模板。先按服务器情况调整项目路径和运行用户；创建 `/etc/tdx-research.env`，配置 `TDX_API_TOKEN` 与 `TDX_VIPDOC_DIR` 并限制文件权限。确保运行用户能读取数据目录、写入 `output/`。服务内部已含调度，无需再增加 crontab 或另起进程写同一个数据库。

模板使用 `tdx` 用户/组，需预先创建，或改成服务器已有的普通服务用户。若自行创建，可用 `sudo useradd --system --home /opt/tdx --shell /usr/sbin/nologin tdx`；只把 `output/` 的写权限交给该用户，源目录保留只读权限。复制 `deploy/tdx-research.env.example` 到 `/etc/tdx-research.env`，在其中设置 `TDX_API_TOKEN` 和服务器实际的 `TDX_VIPDOC_DIR`；systemd 的 `ExecStart` 不再写死数据目录。

```bash
sudo cp deploy/tdx-research.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now tdx-research
journalctl -u tdx-research -f
```

## PM2 管理（Debian 可选）

不要同时用 systemd 和 PM2 启动同一个研究服务。`pm2.config.js` 已增加独立的 `tdx-research` 应用，默认监听 8081，数据库位于 `output/research/market.duckdb`，日志写入 `output/logs/`。管理脚本会先加载 `/etc/tdx-research.env` 并校验令牌、绝对数据路径和可执行文件：

```bash
cd /opt/tdx
chmod +x deploy/tdx-research-pm2.sh
./deploy/tdx-research-pm2.sh start
./deploy/tdx-research-pm2.sh status
./deploy/tdx-research-pm2.sh logs 200

# 首次数据流程
./deploy/tdx-research-pm2.sh down
./deploy/tdx-research-pm2.sh import
./deploy/tdx-research-pm2.sh jobs
./deploy/tdx-research-pm2.sh update
./deploy/tdx-research-pm2.sh daily
```

`down` 使用通达信官方完整日线包下载器，压缩包缓存在 `output/hsjday`，并解压到 `TDX_VIPDOC_DIR`。由于官方压缩包固定包含 `vipdoc/` 根目录，配置路径必须以 `/vipdoc` 结尾。不要在 `import` 正在读取目录时执行 `down`。

修改 `/etc/tdx-research.env` 后使用 `restart` 或 `reload`，脚本会带 `--update-env`。执行 `pm2 startup` 和脚本的 `start` 后，`pm2 save` 会保存进程清单。若环境文件不在 `/etc`，可设置 `TDX_RESEARCH_ENV_FILE=/path/to/file`；API 地址可通过 `TDX_RESEARCH_BASE_URL` 覆盖。

上面的 systemd 示例会占用 8080；若原行情服务也使用 8080，请修改其中一个端口。PM2 配置已将研究服务放在 8081。

## 完整 API 操作顺序

以下在设置了相同 `TDX_API_TOKEN` 的终端执行。日期必须替换成实际数据库覆盖日期。

```bash
BASE=http://192.168.1.74:8080

# 1. 历史入库。路径只使用启动参数，HTTP 不能读取任意服务器目录。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' -d '{}' "$BASE/v1/jobs/import"

# 等 state=succeeded；失败时 errors 给出具体文件/证券。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" "$BASE/v1/jobs"
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" "$BASE/v1/status"

# 2. 小样本补数并获取公司行为；完成后可去掉 symbols 更新当前全市场。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"symbols":["sh600519","sz000001"]}' "$BASE/v1/jobs/update"

# 3. 建立策略。内置 ma_trend / breakout，默认初始化 ma20（MA5/20）。
curl -sS -X PUT -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"MA5/20趋势","kind":"ma_trend","fast":5,"slow":20,"lookback":20,"min_amount":50000000}' \
  "$BASE/v1/strategies/my_ma"

# 4. 查看本地前复权历史；复权锚定 end，不使用 end 之后的事件。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  "$BASE/v1/bars?symbol=sz000001&start=2025-01-01&end=2026-09-16&adjust=qfq"

# 5. 回测。必须有 start 之前的预热历史、成功获取过公司行为。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"strategy_id":"my_ma","config":{"symbol":"sz000001","start":"2025-01-02","end":"2026-09-16","capital":100000,"commission":0.0003,"min_commission":5,"sell_tax":0.0005,"slippage_bps":5,"limit_pct":0.10,"lot":100}}' \
  "$BASE/v1/backtests"

# 6. 同一策略选股；没有当日行情、预热不足、未检查公司行为会列入 excluded。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"strategy_id":"my_ma","date":"2026-09-16"}' "$BASE/v1/screens"

# 7. 盘后复盘：涨跌宽度、成交额、涨跌排名、全部策略候选股。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' -d '{"date":"2026-09-16"}' "$BASE/v1/reviews"

# 或一键执行在线更新 → 信号 → 复盘并保存。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" \
  -H 'Content-Type: application/json' -d '{}' "$BASE/v1/jobs/daily"

# 原有实时报价/集合竞价路由保持原路径。
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" "$BASE/quote?codes=sz000001,sh600519"
curl -sS -H "Authorization: Bearer $TDX_API_TOKEN" "$BASE/call_auction?code=sz000001"
```

`GET /v1/strategies` 列出策略；`GET /v1/artifacts/{id}` 获取保存的选股、复盘、回测结果。回测返回 `dataset_id`，对应完整的固定输入快照，包含原始行情、公司行为和数据版本；结果包含 SHA256、参数和引擎版本，避免后续补数后失去复现依据。`GET /v1/dataset?symbol=sz000001&end=2026-09-16` 导出当前单股 JSON 数据集。v1 未提供 Parquet 导出。

自动任务每天上海时间 19:00 执行 `daily`，重启后会补执行当天未完成任务。失败间隔至少 30 分钟、每天最多 3 次；进度和错误落库，重启把中断任务标为 `interrupted`。没有硬编码节假日表：休市日可能仍检查上游，复盘按实际最新行情日期标注，不伪造当天记录。任务成功只表示请求和入库成功，**不等于上游数据已经覆盖目标交易日**，应同时检查 status 中的末日期。

更新遇到连续五只证券失败会结束本轮并保留进度，避免上游断线时逐只超时拖延数小时。下一轮按每只证券的已存日期继续覆盖补数。

## 数据口径与边界

- 数据库存原始价格（整数厘）、原始成交额（元）；API 返回价格（元），股票/基金量（股/份），指数成交量（手）。在线股票量由 tdx 的手转换为股，接口数据本身可能已经丢失零股精度。
- `.day` 默认 `-price-scale 0`，采用 tdx2db 的品种规则：股票/指数除以 100，基金/B 股除以 1000，不靠价格大小猜测。旧版本文件若编码不同，可分目录显式 `-price-scale 100` 或 `1000` 覆盖。先用客户端校验股票、ETF、指数样本。原有 `extend.ReadDay` 使用统一除以 100，新导入器独立处理这个差异，避免影响旧调用方。
- 参考 tdx2db 的目录批量导入和扩展成交量标记约定；独立实现存储层，不引入其运行时、调度或复权代码。保留其 MIT 署名于 `docs/third-party/tdx2db-LICENSE`。
- 每个文件/证券单独事务提交，主键合并。中途失败可重跑；不会回滚其他已成功证券。导入修订会递增数据版本，即使重复内容也可能产生新版本号。
- 异常记录（如收盘低于最低价）保留原始字节、文件 SHA256、行号和原因，隔离到 `data_issues`，有效记录继续入库。`GET /v1/data-issues?symbol=sh000001` 查询前 1000 条异常；重复导入同一文件不会重复增加异常。存在隔离记录时任务标为 failed 提醒处理，但 `rows` 仍报告已成功提交数量。不会自动“修正”源价格；源文件截断等结构性错误则整文件拒绝。
- 在线更新按每只证券末日期向前重叠 14 个自然日拉取，不以全表最大日期推进。**更早的历史缺口需要重新导入完整离线文件**，不声称能从上游无限补齐。
- 行情与对应公司行为同一事务提交；获取公司行为失败不会发布这只证券的半成品。仅离线导入后，复权/回测默认拒绝未检查的公司行为，选股列明排除原因。
- 再次离线导入会撤销该证券的公司行为“已检查”标记，须重新在线更新后才能复权或回测，避免新导入日期使用过时的除权数据。
- v1 策略为单股做多日线模型：`ma_trend` 快均线大于慢均线入场，反之退出；`breakout` 收盘突破之前 lookback 根最高价入场，跌破 fast 均线退出；min_amount 只限制入场。
- 收盘形成信号、下一根可用日线开盘执行，至少持有到后续交易日。禁止零量、一字板成交；按配置的涨跌幅阈值保守限制买卖。滑点价格超出当日区间时不成交，不会裁剪到有利价格。
- 费用、涨跌幅、交易单位为用户配置的固定假设，示例不是历史规则表。**未实现历史 ST/新股限制、真实排队撮合、完整交易日历、退市清算或多资产组合回测**；不把缺行情自动认定为停牌。
- 公司行为复用 tdx 仿射复权，按信号日生效范围计算。模拟账户在除权日简化记入分红和送股，不处理真实到账日和分红税；区间内配股、缩股拒绝回测，避免静默生成错误收益。
- 复盘范围是本地已存 A 股，非保证完整的全市场；相邻行情间可能跨停牌或缺数日期。v1 无历史板块成分快照、历史财务公告时点数据，不能用来宣称无幸存者偏差的板块/基本面回测。
- 保存的公司行为可能被上游事后修订。按生效日截断可以避免使用未来生效事件，但不能重建过去当时可见的公告修订版本。

## 备份与运维

只有服务进程打开 DuckDB 读写；不要同时运行另一个导入程序或 DuckDB CLI 改写同一文件。最简单的可靠备份是停止服务、复制完整 `output/research/` 到另一块磁盘、再启动；先确认服务正常退出，保留可能存在的 WAL。也要保留原始 vipdoc。

跨全市场的信号查询仅加载所需最近窗口，单股回测加载完整历史。大量请求仍会共享 CPU/内存，请先测量再扩大并发。接口是研究用途，非交易执行或实时撮合系统。

## 可选 Docker

```bash
export TDX_VIPDOC_DIR=/srv/tdx/vipdoc
export TDX_API_TOKEN='替换为自己的足够长随机令牌'
docker compose -f deploy/research.compose.yml up -d --build
```

数据库保存在命名卷，导入目录只读挂载。新 Dockerfile 使用独立 ignore 文件，避免被仓库原 `.dockerignore` 排除 Go 源码。已有原版 Dockerfile/compose 未改动。

## 验证

```bash
CGO_ENABLED=1 go test ./extend/research ./extend/httpserver ./cmd/tdx-research
CGO_ENABLED=1 go test -race ./extend/research
```

测试使用真实内存 DuckDB 和虚构行情，不连接上游，不发送交易请求。覆盖解析、事务合并、失败保护、复权时间截断、次日执行、费用、一字板限制、分红记账、任务和 HTTP 全流程。联网行情和 Debian 实机部署需在目标环境另做验收。
