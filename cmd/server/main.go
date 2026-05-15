package main

import (
	"browserctl/svc/internal/chrome"
	"browserctl/svc/internal/http"
	"browserctl/svc/internal/proxy"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Secret     string
	SvcPort    int
	HttpPort   int
	ProfileDir string
	ExtPath    string
	NoLaunch   bool
}

func loadConfig() Config {
	cfg := Config{
		SvcPort:    9223,
		HttpPort:   9224,
		ProfileDir: chrome.DefaultProfileDir(),
		ExtPath:    chrome.DefaultExtPath(),
	}

	readEnv(&cfg, ".env")
	readEnv(&cfg, "config.json")

	if v := os.Getenv("BROWSERCTL_SECRET"); v != "" {
		cfg.Secret = v
	}
	if v := os.Getenv("BROWSERCTL_SVC_PORT"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 {
			cfg.SvcPort = n
		}
	}
	if v := os.Getenv("BROWSERCTL_HTTP_PORT"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 {
			cfg.HttpPort = n
		}
	}
	if v := os.Getenv("BROWSERCTL_PROFILE_DIR"); v != "" {
		cfg.ProfileDir = v
	}
	if v := os.Getenv("BROWSERCTL_EXT_PATH"); v != "" {
		cfg.ExtPath = v
	}
	if v := os.Getenv("BROWSERCTL_NO_LAUNCH"); v != "" {
		cfg.NoLaunch = v == "true" || v == "1"
	}

	return cfg
}

func readEnv(cfg *Config, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "BROWSERCTL_SECRET":
			cfg.Secret = val
		case "BROWSERCTL_SVC_PORT":
			if n, _ := strconv.Atoi(val); n > 0 {
				cfg.SvcPort = n
			}
		case "BROWSERCTL_HTTP_PORT":
			if n, _ := strconv.Atoi(val); n > 0 {
				cfg.HttpPort = n
			}
		case "BROWSERCTL_PROFILE_DIR":
			cfg.ProfileDir = val
		case "BROWSERCTL_EXT_PATH":
			cfg.ExtPath = val
		case "BROWSERCTL_NO_LAUNCH":
			cfg.NoLaunch = val == "true" || val == "1"
		}
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg := loadConfig()

	logger.Info("browserctl/svc starting",
		"ws", cfg.SvcPort,
		"http", cfg.HttpPort,
		"secret", cfg.Secret != "",
		"profile", cfg.ProfileDir,
		"ext", cfg.ExtPath,
	)

	cdpServer := proxy.NewCdpServer(cfg.SvcPort, cfg.Secret, logger)

	if err := cdpServer.Start(); err != nil {
		logger.Error("failed to start CDP server", "err", err)
		os.Exit(1)
	}

	httpSrv := http.NewServer(cfg.HttpPort, cfg.SvcPort, func() map[string]interface{} {
		return cdpServer.GetStatus()
	})

	go func() {
		srv := httpSrv.Serve()
		logger.Info("http server listening on :" + strconv.Itoa(cfg.HttpPort))
		if err := srv.ListenAndServe(); err != nil {
			logger.Error("http server error", "err", err)
		}
	}()

	if !cfg.NoLaunch && cfg.ExtPath != "" {
		launcher := chrome.NewLauncher(logger, cfg.ProfileDir, cfg.ExtPath)
		if err := launcher.Launch(); err != nil {
			logger.Warn("Chrome launch failed", "err", err)
		}
	}

	logger.Info("browserctl/svc ready", "ws", cfg.SvcPort, "http", cfg.HttpPort)

	select {}
}