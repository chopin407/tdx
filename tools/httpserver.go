package main

import (
	"log"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend/httpserver"
)

func main() {

	s, err := httpserver.New(
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