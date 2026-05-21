.PHONY: all build install uninstall start stop restart lint test

GO=go
GOFLAGS=-ldflags="-s -w"
SCRIPTS_DIR=$(shell pwd)/scripts
OS=$(shell uname -s)

BINARY=bin/browserctl-svc

all: build

build:
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/svc

lint:
	golangci-lint run ./...

test:
	$(GO) test ./...

install: build
	@case "$(OS)" in \
		Linux*)  $(SCRIPTS_DIR)/install-linux.sh ;; \
		Darwin*) $(SCRIPTS_DIR)/install-macos.sh ;; \
		*)       echo "Unsupported OS: $(OS)" >&2; exit 1 ;; \
	esac

uninstall:
	@case "$(OS)" in \
		Linux*)  $(SCRIPTS_DIR)/uninstall-linux.sh ;; \
		Darwin*) $(SCRIPTS_DIR)/uninstall-macos.sh ;; \
		*)       echo "Unsupported OS: $(OS)" >&2; exit 1 ;; \
	esac

start:
	@case "$(OS)" in \
		Linux*)  sudo systemctl start browserctl-svc && echo "browserctl-svc started" ;; \
		Darwin*) \
			PID=$$(launchctl list com.browserctl.svc 2>/dev/null | grep -o '"PID" = [0-9]*' | grep -o '[0-9]*'); \
			if [ -n "$$PID" ] && [ "$$PID" != "0" ]; then \
				echo "browserctl-svc already running (PID: $$PID)"; \
			else \
				launchctl load ~/Library/LaunchAgents/com.browserctl.svc.plist 2>/dev/null; \
				sleep 1; \
				PID=$$(launchctl list com.browserctl.svc 2>/dev/null | grep -o '"PID" = [0-9]*' | grep -o '[0-9]*'); \
				if [ -n "$$PID" ] && [ "$$PID" != "0" ]; then \
					echo "browserctl-svc started"; \
				else \
					echo "browserctl-svc failed to start (port in use?)"; \
				fi \
			fi \
			;; \
		*)       echo "Unsupported OS: $(OS)" >&2; exit 1 ;; \
	esac

stop:
	@case "$(OS)" in \
		Linux*)  sudo systemctl stop browserctl-svc && echo "browserctl-svc stopped" ;; \
		Darwin*) launchctl unload ~/Library/LaunchAgents/com.browserctl.svc.plist && echo "browserctl-svc stopped" ;; \
		*)       echo "Unsupported OS: $(OS)" >&2; exit 1 ;; \
	esac

restart:
	@case "$(OS)" in \
		Linux*)  sudo systemctl restart browserctl-svc && echo "browserctl-svc restarted" ;; \
		Darwin*) launchctl unload ~/Library/LaunchAgents/com.browserctl.svc.plist 2>/dev/null || true; launchctl load ~/Library/LaunchAgents/com.browserctl.svc.plist && echo "browserctl-svc restarted" ;; \
		*)       echo "Unsupported OS: $(OS)" >&2; exit 1 ;; \
	esac