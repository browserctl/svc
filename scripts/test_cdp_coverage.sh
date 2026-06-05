#!/usr/bin/env bash
# =============================================================================
# CDP Interface Coverage Test
# =============================================================================
# Tests that browserctl-svc correctly proxies CDP commands across ALL domains
# declared in getDomains() (ws_server.go). Uses a mock WebSocket server to
# simulate the Chrome extension side, so no real Chrome is needed.
#
# Usage: ./test_cdp_coverage.sh [--watch]
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WS_SERVER_PATH="$SCRIPT_DIR/../internal/proxy/ws_server.go"
SVC_PORT="${SVC_PORT:-9222}"
MOCK_EXT_PORT="${MOCK_EXT_PORT:-9333}"
WS_MODULE="/Users/bin/workspace/skills/chrome-use/node_modules/ws"

# Domains from getDomains() in ws_server.go (hardcoded - do NOT fetch protocol.json)
DOMAINS=(
  "Browser"
  "Target"
  "Page"
  "Runtime"
  "DOM"
  "DOMDebugger"
  "Network"
  "Input"
  "Log"
  "Fetch"
  "Emulation"
  "Security"
  "Accessibility"
  "Performance"
  "LayerTree"
  "Storage"
  "Schema"
)

# Minimal commands per domain to test passthrough (not Chrome correctness)
# Each entry: domain:method
COMMANDS=(
  "Browser.getVersion"
  "Browser.getBrowserCommandLine"
  "Browser.close"
  "Target.getTargets"
  "Target.setDiscoverTargets"
  "Page.getLayoutMetrics"
  "Page.captureScreenshot"
  "Runtime.evaluate"
  "Runtime.getProperties"
  "DOM.getDocument"
  "DOMDebugger.getDOMBreakpoints"
  "Network.canClearBrowserCache"
  "Input.insertText"
  "Log.enable"
  "Fetch.canClearBrowserCache"
  "Emulation.canClearDeviceMetricsOverride"
  "Security.canClearCertificateErrors"
  "Accessibility.getFullAXTree"
  "Performance.getMetrics"
  "LayerTree.enable"
  "Storage.clearDataForOrigin"
  "Schema.getDomains"
)

# =============================================================================
# Helper: wait for service
# =============================================================================

wait_for_port() {
  local port=$1
  local timeout=${2:-10}
  local elapsed=0
  while ! nc -z localhost "$port" 2>/dev/null; do
    sleep 0.5
    elapsed=$((elapsed + 1))
    if ((elapsed >= timeout)); then
      echo "ERROR: port $port not available after ${timeout}s"
      return 1
    fi
  done
}

# =============================================================================
# Check prerequisites
# =============================================================================

check_prereqs() {
  if ! command -v node &>/dev/null; then
    echo "ERROR: node not found"
    exit 1
  fi
  if [[ ! -d "$WS_MODULE" ]]; then
    echo "ERROR: ws module not found at $WS_MODULE"
    echo "Installing chrome-use dependencies..."
    (cd /Users/bin/workspace/skills/chrome-use && npm install --silent 2>&1 | tail -5 || true)
    if [[ ! -d "$WS_MODULE" ]]; then
      echo "ERROR: ws module still not found"
      exit 1
    fi
  fi
}

# =============================================================================
# Start mock extension WebSocket server
# This simulates the Chrome extension side of the protocol:
# - register, tabs_list, and cdp_command responses
# =============================================================================

start_mock_extension() {
  local port=$1
  cat > /tmp/mock_extension.mjs << 'MOCK_EOF'
import pkg from '/Users/bin/workspace/skills/chrome-use/node_modules/ws/index.js';
const { WebSocketServer } = pkg;

const PORT = parseInt(process.argv[2] || '9333');
const wss = new WebSocketServer({ port: PORT });
console.log(`[Mock Extension] listening on port ${PORT}`);

let extWs = null;
let nextId = 1;
const pending = new Map();

wss.on('connection', (ws) => {
  console.log('[Mock Extension] browserctl-svc connected');
  extWs = ws;

  ws.on('message', (data) => {
    try {
      const msg = JSON.parse(data.toString());
      console.log(`[Mock Extension] → ${msg.type || msg.method || '?'}`);
      handleMessage(ws, msg);
    } catch (e) {
      console.error('[Mock Extension] parse error:', e.message);
    }
  });

  ws.on('close', () => {
    console.log('[Mock Extension] disconnected');
    extWs = null;
  });
});

function send(ws, obj) {
  if (ws && ws.readyState === 1) ws.send(JSON.stringify(obj));
}

function handleMessage(ws, msg) {
  const reqId = msg.id || 0;

  switch (msg.type) {
    case 'register':
      send(ws, { type: 'register', windowId: 9999, role: 'mock' });
      break;

    case 'ping':
      send(ws, { type: 'pong' });
      break;

    case 'tabs_list':
      send(ws, {
        type: 'tabs_list',
        tabs: [
          { id: 1, title: 'Mock Tab', url: 'https://example.com', active: true },
          { id: 2, title: 'Tab 2', url: 'https://example.org', active: false }
        ]
      });
      break;

    case 'cdp_command': {
      // Echo back matching id so svc's pending callback resolves
      send(ws, {
        type: 'cdp_result',
        id: reqId,
        success: true,
        result: buildFakeResult(msg.method)
      });
      break;
    }

    case 'tab_attach': {
      send(ws, { type: 'tab_attach_result', id: reqId, success: true });
      break;
    }

    case 'new_tab':
      send(ws, { type: 'new_tab_result', id: reqId, tab: { id: 99, title: 'New Tab' }, success: true });
      break;

    default:
      console.log(`[Mock Extension] unhandled: ${msg.type}`);
  }
}

function buildFakeResult(method) {
  if (!method) return {};
  if (method.includes('getVersion')) {
    return { protocolVersion: '1.3', product: 'Chrome/999.0.0', revision: '@mock', userAgent: '', jsVersion: '' };
  }
  if (method.includes('getTargets')) {
    return { targetInfos: [{ targetId: 'tab-1', type: 'page', title: 'Mock', url: 'https://example.com', attached: true }] };
  }
  if (method.includes('getDocument')) {
    return { root: { nodeId: 1, backendNodeId: 1, nodeName: 'HTML', nodeType: 10 } };
  }
  if (method.includes('evaluate')) {
    return { result: { type: 'string', value: 'mock_evaluated' }, exceptionDetails: null };
  }
  if (method.includes('getMetrics')) {
    return { metrics: [{ name: 'Documents', value: '1' }] };
  }
  if (method.includes('getAXTree')) {
    return { nodes: [] };
  }
  if (method.includes('getFullAXTree')) {
    return { nodes: [] };
  }
  return { success: true };
}

process.on('SIGTERM', () => { wss.close(); process.exit(0); });
process.on('exit', () => wss.close());
MOCK_EOF

  node /tmp/mock_extension.mjs "$port" &
  local pid=$!
  echo $pid > /tmp/mock_extension.pid
  sleep 1

  if ! kill -0 $pid 2>/dev/null; then
    echo "ERROR: mock extension failed to start"
    return 1
  fi
  echo "[Mock Extension] started PID $pid"
}

stop_mock_extension() {
  if [[ -f /tmp/mock_extension.pid ]]; then
    local pid=$(cat /tmp/mock_extension.pid)
    kill "$pid" 2>/dev/null || true
    rm -f /tmp/mock_extension.pid
  fi
}

# =============================================================================
# Check / start browserctl-svc
# =============================================================================

check_svc() {
  if nc -z localhost "$SVC_PORT" 2>/dev/null; then
    echo "[browserctl-svc] already running on port $SVC_PORT"
    return 0
  fi

  echo "[browserctl-svc] not detected, attempting to start..."
  if command -v browserctl-svc &>/dev/null; then
    browserctl-svc &
    local svc_pid=$!
    echo "[browserctl-svc] started PID $svc_pid"
    wait_for_port "$SVC_PORT" 10
    sleep 1
  else
    echo "WARNING: browserctl-svc not found in PATH"
    echo "  Some tests may fail if service is not running"
  fi
}

# =============================================================================
# Run CDP coverage test using Node.js + ws
# =============================================================================

run_test() {
  cat > /tmp/test_cdp_coverage.mjs << 'TEST_EOF'
import pkg from '/Users/bin/workspace/skills/chrome-use/node_modules/ws/index.js';
const { WebSocket } = pkg;

const SVC_PORT = process.argv[2] || '9222';
const WS_URL = `ws://localhost:${SVC_PORT}/devtools/browser`;
const TIMEOUT = 10000;

const domains = [
  'Browser', 'Target', 'Page', 'Runtime', 'DOM', 'DOMDebugger',
  'Network', 'Input', 'Log', 'Fetch', 'Emulation', 'Security',
  'Accessibility', 'Performance', 'LayerTree', 'Storage', 'Schema'
];

// Minimal command set per domain — verifies svc PASSES command through
// (not whether Chrome responds correctly)
const commands = [
  'Browser.getVersion',
  'Browser.getBrowserCommandLine',
  'Browser.close',
  'Target.getTargets',
  'Target.setDiscoverTargets',
  'Page.getLayoutMetrics',
  'Page.captureScreenshot',
  'Runtime.evaluate',
  'Runtime.getProperties',
  'DOM.getDocument',
  'DOMDebugger.getDOMBreakpoints',
  'Network.canClearBrowserCache',
  'Input.insertText',
  'Log.enable',
  'Fetch.canClearBrowserCache',
  'Emulation.canClearDeviceMetricsOverride',
  'Security.canClearCertificateErrors',
  'Accessibility.getFullAXTree',
  'Performance.getMetrics',
  'LayerTree.enable',
  'Storage.clearDataForOrigin',
  'Schema.getDomains',
];

const results = { passed: [], failed: [], errors: [] };

function send(ws, msg, sessionId = null) {
  return new Promise((resolve, reject) => {
    const id = Math.floor(Math.random() * 1e9) + 1;
    const payload = { id, method: msg.method, params: msg.params || {} };
    if (sessionId) payload.sessionId = sessionId;

    const timer = setTimeout(() => {
      clearTimeout(timer);
      reject(new Error(`TIMEOUT: ${msg.method}`));
    }, TIMEOUT);

    const msgHandler = (data) => {
      try {
        const resp = JSON.parse(data.toString());
        if (resp.id === id) {
          clearTimeout(timer);
          ws.removeListener('message', msgHandler);
          resolve(resp);
        }
      } catch (_) {}
    };
    ws.on('message', msgHandler);

    ws.send(JSON.stringify(payload));
  });
}

async function main() {
  let ws;
  try {
    console.log(`[Test] Connecting to ${WS_URL}...`);
    ws = new WebSocket(WS_URL);
    await new Promise((res, rej) => { ws.on('open', res); ws.on('error', rej); });
    console.log('[Test] Connected\n');

    let sessionId = null;

    // First, call Schema.getDomains to verify domains match
    try {
      const domainsResp = await send(ws, { method: 'Schema.getDomains' });
      if (domainsResp.result?.domains) {
        const actual = domainsResp.result.domains.map(d => d.name);
        console.log(`[Test] Schema.getDomains returned ${actual.length} domains`);
        const match = actual.length === domains.length;
        console.log(`[Test] Domain count match: ${match ? 'PASS' : 'FAIL'} (expected ${domains.length}, got ${actual.length})`);
      }
    } catch (e) {
      console.log(`[Test] Schema.getDomains error: ${e.message}`);
    }

    // Try to attach to a target to get a sessionId
    try {
      const targets = await send(ws, { method: 'Target.getTargets' });
      if (targets.result?.targetInfos?.length > 0) {
        const tid = targets.result.targetInfos[0].targetId;
        const attach = await send(ws, { method: 'Target.attachToTarget', params: { targetId: tid } });
        if (attach.result?.sessionId) {
          sessionId = attach.result.sessionId;
          console.log(`[Test] Attached to target ${tid}, sessionId=${sessionId}`);
        }
      }
    } catch (e) {
      console.log(`[Test] Target attach (non-fatal): ${e.message}`);
    }

    // Send each command
    console.log('\n[Test] Sending CDP commands...\n');
    for (const method of commands) {
      const [domain, cmd] = method.split('.');
      try {
        const resp = await send(ws, { method }, sessionId);
        if (resp.error) {
          results.failed.push({ method, error: resp.error.message || JSON.stringify(resp.error) });
          console.log(`[FAIL] ${method}: ${resp.error.message || JSON.stringify(resp.error)}`);
        } else {
          results.passed.push({ method });
          console.log(`[PASS] ${method}`);
        }
      } catch (e) {
        results.errors.push({ method, error: e.message });
        console.log(`[ERR]  ${method}: ${e.message}`);
      }
    }

    ws.close();
  } catch (e) {
    console.error('[Test] Connection error:', e.message);
    process.exit(1);
  }

  // Print summary
  console.log('\n========================================');
  console.log('CDP Coverage Results');
  console.log('========================================');
  console.log(`PASSED:  ${results.passed.length}/${commands.length}`);
  console.log(`FAILED:  ${results.failed.length}/${commands.length}`);
  console.log(`ERROR:   ${results.errors.length}/${commands.length}`);

  if (results.failed.length > 0) {
    console.log('\nFailed commands:');
    for (const r of results.failed) {
      console.log(`  ${r.method}: ${r.error}`);
    }
  }
  if (results.errors.length > 0) {
    console.log('\nErrored commands:');
    for (const r of results.errors) {
      console.log(`  ${r.method}: ${r.error}`);
    }
  }

  const totalFailures = results.failed.length + results.errors.length;
  process.exit(totalFailures > 0 ? 1 : 0);
}

main();
TEST_EOF

  node /tmp/test_cdp_coverage.mjs "$SVC_PORT"
}

# =============================================================================
# Main
# =============================================================================

main() {
  echo "========================================"
  echo "CDP Interface Coverage Test"
  echo "========================================"
  echo "Service port:    $SVC_PORT"
  echo "Mock ext port:   $MOCK_EXT_PORT"
  echo "Domains:         ${DOMAINS[*]}"
  echo "Commands:        ${#COMMANDS[@]}"
  echo "========================================"

  check_prereqs

  stop_mock_extension 2>/dev/null || true
  check_svc

  # Start mock extension server
  start_mock_extension "$MOCK_EXT_PORT"

  # Give svc a moment to register the mock extension
  sleep 2

  # Run the test
  run_test
  local exit_code=$?

  stop_mock_extension

  echo ""
  if ((exit_code == 0)); then
    echo "TEST PASSED"
  else
    echo "TEST FAILED (exit $exit_code)"
  fi

  exit $exit_code
}

main "$@"