# 第一阶段：构建阶段（仅复制本地二进制）
# FROM alpine:3.20 AS builder
# builder 版本对齐本机 go1.27.1
FROM golang:1.27-alpine AS builder

WORKDIR /app
# 复制本机编译好的linux amd64二进制
COPY httpserver ./httpserver
# 给执行权限（本机已经有+x，镜像内再加固一次）
RUN chmod +x ./httpserver

# 最终运行镜像，alpine轻量
FROM alpine:3.20
WORKDIR /app

# alpine缺少glibc，Go静态编译二进制需要安装libc兼容包
RUN apk add --no-cache libc6-compat

COPY --from=builder /app/httpserver ./httpserver

# 对外暴露端口，请确认你的httpserver实际监听端口，示例用8080
EXPOSE 8080

# 启动命令
ENTRYPOINT ["./httpserver"]
