# tdx TCP 长连接服务

本包把 `extend/httpserver` 的完整只读行情 API 暴露为 TCP 长连接服务。TCP 与 HTTP 共用同一套路由和参数校验，因此覆盖标准行情、K 线、复权、分时成交、财务/F10、板块/行业/资金流以及扩展行情。

## 协议

每条消息为 `4 字节大端序长度 + UTF-8 JSON`。同一连接允许并发请求，响应顺序不保证一致，使用 `id` 关联。

```json
{"id":"1","action":"quote","token":"secret","params":{"codes":["sz000001","sh600519"]}}
```

```json
{"id":"1","status":200,"code":0,"msg":"ok","data":[...]}
```

`action` 等于 HTTP 路径去掉开头 `/`，例如 `kline/day`、`kline/day/qfq`、`ex/quote`。`ping` 和 `health` 映射健康检查。参数名称和完整接口清单见 `../httpserver/README.md`；数组参数会转换为逗号分隔字符串。

## 启动服务

```go
package main

import (
	"log"
	"time"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend/httpserver"
	"github.com/injoyai/tdx/extend/tcpserver"
)

func main() {
	s, err := tcpserver.Default(
		[]tcpserver.Option{
			tcpserver.WithAddr(":8090"),
			tcpserver.WithToken("secret"),
			tcpserver.WithMaxConnections(256),
			tcpserver.WithMaxInflight(16),
			tcpserver.WithIdleTimeout(2 * time.Minute),
		},
		httpserver.WithPoolSize(8),
		httpserver.WithExHqHosts(tdx.ExHosts...),
		httpserver.WithExPoolSize(2),
	)
	if err != nil { log.Fatal(err) }
	defer s.Close()
	log.Fatal(s.Run())
}
```

## Go 客户端

```go
c, err := tcpserver.Dial("192.168.1.74:8090", "secret", 5*time.Second)
if err != nil { panic(err) }
defer c.Close()

var data json.RawMessage
err = c.Call(context.Background(), "kline/day", map[string]any{
	"code": "sh600519", "start": 0, "count": 100,
}, &data)
```

生产环境应配置访问令牌；跨不可信网络时应在 TLS/VPN 后使用。大文件和全市场 `gbbq/all` 响应可能超过默认 16 MiB，可用 `WithMaxFrame` 调整，但更推荐分页或按代码分批查询。
