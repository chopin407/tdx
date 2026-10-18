# 历史数据服务（DuckDB）

tdx 负责完整日线包下载、离线入库、在线增量更新、公司行为与证券档案补齐、数据覆盖率检查，以及历史 K 线和实时行情查询。不再运行策略、选股、执行校验或回测；这些功能归 a-stock-dashboard 管理。可执行文件和既有数据库路径保持原名，以兼容部署。已有 DuckDB 中的旧策略及回测表不会自动删除，但新版本不创建、不读写、不暴露这些内容。

## 配置与启动

离线目录通过绝对路径环境变量 `TDX_VIPDOC_DIR` 配置，命令行 `-import-dir` 优先。服务需要至少 16 位的 `TDX_API_TOKEN`。DuckDB Go 驱动需要 CGO 和 C/C++ 编译器。

```bash
cd /opt/tdx
mkdir -p output/bin output/research
CGO_ENABLED=1 go build -trimpath -o output/bin/tdx-research ./cmd/tdx-research
go build -trimpath -o output/bin/tdx-down ./cmd/tdx-down
export TDX_VIPDOC_DIR=/data/tdx/vipdoc
export TDX_API_TOKEN='<从安全环境文件读取的令牌>'
./output/bin/tdx-research -addr :8081 -db output/research/market.duckdb -schedule 19:00
```

生产环境可使用 `deploy/tdx-research.service` 或 `deploy/tdx-research-pm2.sh`，二者不可同时打开同一 DuckDB。PM2 默认从 `/etc/tdx-research.env` 读取配置；`TDX_RESEARCH_ENV_FILE` 可覆盖。服务读取 vipdoc 目录、写入 output；不要在导入过程中改写完整包。

## 数据任务

```bash
./deploy/tdx-research-pm2.sh down    # 下载并解压官方完整日线包
./deploy/tdx-research-pm2.sh import  # 原始日线入库
./deploy/tdx-research-pm2.sh update  # 补齐在线日线、公司行为和当前证券档案
./deploy/tdx-research-pm2.sh daily   # 每日增量更新，仅数据，无复盘产物
./deploy/tdx-research-pm2.sh jobs
```

服务同一时间只运行一个写入任务。上海时间 16:30 前不会把当日尚未完成的日线入库；`-schedule 19:00` 默认调度每日更新。离线包不含完整公司行为及当前证券档案，前复权查询前应运行 `update`。

### 每日调度与失败补齐

- 自动调度使用上海时间，周末跳过；工作日沿用 `Workday` 的指数日线判定依据，先通过 TDX 上证指数 `sh000001` 确认当天有日线。节假日、断网或当日日线尚未发布均不启动全库任务，30分钟后可重新确认。手动任务仍允许在休市日补历史数据。
- 每个自然日最多三次自动任务；失败后从**结束时间**起等待至少30分钟，仅补失败及未处理证券，不重新扫描已成功证券。任务中断的剩余名单也持久化保存。旧任务仅在错误名单完整时支持定向补齐，不猜测缺失名单。
- 状态：`succeeded` 全部成功；`partial` 部分成功（成功入库不回滚，也可能是日线已入库但档案失败）；`failed` 无成功数据或初始化失败；`interrupted` 服务重启前未完成。`rows` 包含重叠刷新行，并非净新增行。
- 在线任务增加 `total`、`updated`、`last_symbol`、`retry_of`、`retry_symbols` 和运行时 `remaining_symbols`；前端显示北京时间、进度、耗时和错误明细。错误明细最多500条，完整待补证券名单不截断。旧记录保持原始状态，页面可注释为“部分成功（旧记录）”。
- 单次任务默认最多2小时，可用 `-job-timeout 3h` 调整。超时取消网络访问并保留进度；超过5分钟无进度仅为页面核查提示，并不自动判定失败或创建并行任务。

## API

所有端点和原有实时行情路由均使用 `Authorization: Bearer <TDX_API_TOKEN>`：

| 路由 | 用途 |
| --- | --- |
| `GET /v1/health` | 数据库健康状态 |
| `GET /v1/status` | 各证券日线起止与复权核验状态 |
| `GET /v1/data-coverage` | 日线、档案、公司行为和隔离记录覆盖率 |
| `GET /v1/data-issues?symbol=sz000001` | 损坏源记录隔离明细 |
| `GET /v1/jobs` | 后台数据任务状态 |
| `POST /v1/jobs/import`、`update`、`daily` | 提交数据任务，JSON 请求体 `{"symbols":[]}` |
| `GET /v1/bars?symbol=sz000001&end=2026-09-28&adjust=qfq` | 原始/前复权/后复权日线，可加 `start` |
| `GET /v1/history/bars?symbol=sz000001&start=2026-01-01&end=2026-09-28&limit=200` | DuckDB 日线按日期倒序分页读取，响应中的 K 线保持时间升序 |
| `GET /v1/dataset?symbol=sz000001&end=2026-09-28` | 原始数据及公司行为快照 |

`adjust` 可为 `none`、`qfq` 或 `hfq`；公司行为未核验时复权接口返回 409。旧策略、MTF-A、回测和产物路由已移除，调用会得到 404。历史旧表没有自动 DROP，避免破坏用户已有数据；若要清理旧数据库，请另行备份和迁移。

`/v1/history/bars` 的 `symbol` 必填（如 `sz000001`），`start`/`end` 为包含端点的日期，`end` 省略时默认上海时间最近已收定日；`limit` 默认 200、最大 1000。`before` 是排他的日期游标：首次响应若 `has_more=true`，下一页将 `next_before` 作为 `before` 传入，并保持相同 `end`、`adjust` 与 `start`。响应的 `dataset_version` 应作为后续页的 `version` 传回；期间数据库更新则返回 409，须从第一页重查。原始日线直接 SQL 限量读取；复权日线须读取该证券截至 `end` 的完整历史，在同一结束日锚定复权后分页，故请求成本更高。该接口仅提供日线，不混入周/月聚合。
