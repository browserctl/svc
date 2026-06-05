package main

import (
	stdhttp "net/http"

	"browserctl/svc/internal/chrome"
	browserhttp "browserctl/svc/internal/http"
	"browserctl/svc/internal/proxy"
	"flag"
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
	Backend    string // "extension" or "direct"
	CdpUrl     string // used only for direct backend
}

func loadConfig() Config {
	cfg := Config{
		SvcPort:    9222,
		HttpPort:   9223,
		ProfileDir: chrome.DefaultProfileDir(),
		Backend:    "extension",
		CdpUrl:     "http://localhost:9225",
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
	if v := os.Getenv("BROWSERCTL_BACKEND"); v != "" {
		cfg.Backend = v
	}
	if v := os.Getenv("BROWSERCTL_CDP_URL"); v != "" {
		cfg.CdpUrl = v
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
		case "BROWSERCTL_BACKEND":
			cfg.Backend = val
		case "BROWSERCTL_CDP_URL":
			cfg.CdpUrl = val
		}
	}
}

func main() {
	backendFlag := flag.String("backend", "", "CDP backend: extension (default) or direct")
	cdpUrlFlag := flag.String("cdp-url", "", "CDP root URL for direct backend (e.g. http://localhost:9336)")
	portFlag := flag.Int("port", 0, "TCP port for the CDP WebSocket server (default: from config)")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	flag.Parse()

	cfg := loadConfig()

	// CLI flags override env/config
	if *backendFlag != "" {
		cfg.Backend = *backendFlag
	}
	if *cdpUrlFlag != "" {
		cfg.CdpUrl = *cdpUrlFlag
	}
	if *portFlag != 0 {
		cfg.SvcPort = *portFlag
	}

	if cfg.Backend != "extension" && cfg.Backend != "direct" {
		logger.Error("invalid backend", "backend", cfg.Backend, "valid", "extension|direct")
		os.Exit(1)
	}

	logger.Info("browserctl/svc starting",
		"ws", cfg.SvcPort,
		"http", cfg.HttpPort,
		"secret", cfg.Secret != "",
		"profile", cfg.ProfileDir,
		"backend", cfg.Backend,
	)

	cdpServer := proxy.NewCdpServer(cfg.SvcPort, cfg.Secret, logger)
	cdpServer.SetProfileDir(cfg.ProfileDir)

	// Set the backend provider
	var backend proxy.BackendProvider
	switch cfg.Backend {
	case "extension":
		backend = proxy.NewExtensionBackend(logger)
		logger.Info("using ExtensionBackend (production)")
	case "direct":
		backend = proxy.NewDirectCDPBackend(logger, cfg.CdpUrl)
		logger.Info("using DirectCDPBackend (testing)", "cdp-url", cfg.CdpUrl)
	}
	cdpServer.SetBackend(backend)

	if err := cdpServer.Start(); err != nil {
		logger.Error("failed to start CDP server", "err", err)
		os.Exit(1)
	}

	httpSrv := browserhttp.NewServer(cfg.HttpPort, cfg.SvcPort, func() map[string]interface{} {
		return cdpServer.GetStatus()
	})

	go func() {
		srv := httpSrv.Serve()
		logger.Info("http server listening on :" + strconv.Itoa(cfg.HttpPort))
		if err := srv.ListenAndServe(); err != nil && err != stdhttp.ErrServerClosed {
			logger.Error("http server error", "err", err, "port", cfg.HttpPort)
			os.Exit(1)
		}
	}()

	logger.Info("browserctl/svc ready", "ws", cfg.SvcPort, "http", cfg.HttpPort)

	select {}
}