module.exports = {
  apps: [
    {
      name: "tdx-httpserver",
      script: "./httpserver",
      cwd: "/home/kjg/work/tdx",
      instances: 1,
      exec_mode: "fork",
      // 启动参数，没有就注释掉
      // args: ["--host","0.0.0.0","--port","8080"],
      autorestart: true,
      watch: false, // 代码更新不需要自动重启，关闭
      max_memory_restart: "500M",
    //   env: {
    //     // Go环境变量，把代理带上
    //     GOPROXY: "https://goproxy.cn,direct"
    //   },
      log_date_format: "YYYY‑MM‑DD HH:mm:ss",
      out_file: "./logs/out.log",
      error_file: "./logs/err.log"
    }
  ]
};
