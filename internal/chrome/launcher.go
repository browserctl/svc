package chrome

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Launcher struct {
	logger     *slog.Logger
	profileDir string
	extPath    string
	chromePath string
}

func NewLauncher(logger *slog.Logger, profileDir, extPath string) *Launcher {
	return &Launcher{
		logger:     logger,
		profileDir: profileDir,
		extPath:    extPath,
		chromePath: DetectChrome(),
	}
}

func DetectChrome() string {
	if runtime.GOOS == "darwin" {
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if runtime.GOOS == "windows" {
		return "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"
	}
	return "google-chrome"
}

func (l *Launcher) Launch() error {
	if l.chromePath == "" {
		return fmt.Errorf("chrome not found")
	}

	args := []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		fmt.Sprintf("--user-data-dir=%s", l.profileDir),
	}

	if l.extPath != "" {
		args = append(args, fmt.Sprintf("--load-extension=%s", l.extPath))
	}

	cmd := exec.Command(l.chromePath, args...)
	if err := cmd.Run(); err != nil {
		l.logger.Error("Chrome exited with error", "err", err)
		return err
	}

	l.logger.Info("Chrome launching", "path", l.chromePath, "profile", l.profileDir, "ext", l.extPath)
	return nil
}

func (l *Launcher) ChromePath() string {
	return l.chromePath
}

func (l *Launcher) ProfileDir() string {
	return l.profileDir
}

func DefaultProfileDir() string {
	home := os.Getenv("HOME")
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library/Application Support/Google/Chrome")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Google/Chrome/User Data")
	}
	return filepath.Join(home, ".config/google-chrome")
}

func DefaultExtPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)

	// Try: same dir as binary, then parent dirs
	candidates := []string{
		filepath.Join(dir, "ext", "chromium"),
		filepath.Join(dir, "..", "ext", "chromium"),
		filepath.Join(dir, "..", "..", "ext", "chromium"),
		filepath.Join(dir, "..", "..", "..", "ext", "chromium"),
	}

	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			manifest := filepath.Join(p, "manifest.json")
			if _, err := os.Stat(manifest); err == nil {
				return p
			}
		}
	}
	return ""
}