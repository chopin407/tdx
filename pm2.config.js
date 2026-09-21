const path = require("path");

const root = __dirname;

module.exports = {
  apps: [
    {
      name: "tdx-httpserver",
      script: "./httpserver",
      cwd: root,
      instances: 1,
      exec_mode: "fork",
      autorestart: true,
      watch: false,
      max_memory_restart: "500M",
      log_date_format: "YYYY-MM-DD HH:mm:ss",
      out_file: path.join(root, "output/logs/httpserver.out.log"),
      error_file: path.join(root, "output/logs/httpserver.err.log"),
    },
    {
      name: "tdx-research",
      script: "./output/bin/tdx-research",
      cwd: root,
      args: ["-addr", ":8081", "-db", "output/research/market.duckdb", "-schedule", "19:00"],
      instances: 1,
      exec_mode: "fork",
      interpreter: "none",
      autorestart: true,
      restart_delay: 5000,
      kill_timeout: 120000,
      listen_timeout: 15000,
      watch: false,
      max_memory_restart: "1500M",
      env: {
        TZ: "Asia/Shanghai",
        TDX_API_TOKEN: process.env.TDX_API_TOKEN,
        TDX_VIPDOC_DIR: process.env.TDX_VIPDOC_DIR,
      },
      log_date_format: "YYYY-MM-DD HH:mm:ss",
      out_file: path.join(root, "output/logs/tdx-research.out.log"),
      error_file: path.join(root, "output/logs/tdx-research.err.log"),
      merge_logs: true,
    },
  ],
};
