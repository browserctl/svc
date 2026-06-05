#!/bin/bash
set -e

# Isolated port scheme
# Chrome CDP: 9336 (no extension, headless)
# svc direct mode: 9323

SVC_BIN="/Users/bin/code/browserctl/svc/bin/browserctl-svc"
LOG="/tmp/browserctl-direct-test.log"
WS_MOD="/Users/bin/workspace/skills/chrome-use/node_modules/ws"

cleanup() {
    echo "[cleanup]"
    pkill -f "Chrome.*--remote-debugging-port=9336" 2>/dev/null || true
    pkill -f "browserctl-svc.*--svc-port=9323" 2>/dev/null || true
}
trap cleanup EXIT
cleanup
sleep 1

echo "=== [1] Starting Chrome (CDP-only, port 9336) ==="
rm -rf /tmp/cdp-test-direct
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    --headless=new \
    --no-first-run \
    --user-data-dir=/tmp/cdp-test-direct \
    --remote-debugging-port=9336 \
    --disable-extensions \
    --disable-gpu \
    2>/dev/null &
sleep 3

echo "=== [2] Verify Chrome CDP root ==="
curl --noproxy localhost -s "http://localhost:9336/json" | python3 -c "
import json,sys
tabs = json.load(sys.stdin)
page = next((t for t in tabs if t['type']=='page'), None)
if page:
    print('page_id:', page['id'])
    print('ws_url:', page['webSocketDebuggerUrl'])
else:
    print('no page tab')
    sys.exit(1)
" || { echo "Chrome CDP not available"; exit 1; }

echo "=== [3] Starting svc direct mode (port 9323) ==="
BROWSERCTL_BACKEND=direct \
BROWSERCTL_CDP_URL=http://localhost:9336 \
BROWSERCTL_SVC_PORT=9323 \
    $SVC_BIN serve 2>&1 | tee "$LOG" &
SVC_PID=$!
sleep 2
echo "svc PID: $SVC_PID"

echo "=== [4] Test CDP commands through svc (raw JSON-RPC) ==="
node - <<EOF
const WebSocket = require('${WS_MOD}');
const SVC = 'ws://localhost:9323';

// Send raw JSON-RPC (no wrapper) — matches what Playwright sends
function rpc(ws, id, method, params) {
    return new Promise((resolve, reject) => {
        const msg = JSON.stringify({ id, method, params: params || {} });
        const timer = setTimeout(() => reject(new Error(\`\${method} timeout\`)), 8000);
        ws.on('message', (raw) => {
            const m = JSON.parse(raw.toString());
            if (m.id !== id) return; // ignore notifications
            clearTimeout(timer);
            resolve(m);
        });
        ws.send(msg);
    });
}

async function run() {
    // Connect to the CDP path (svc serves WS on /devtools/browser)
    const ws = new WebSocket(SVC + '/devtools/browser');
    await new Promise((r, re) => { ws.on('open', r); ws.on('error', re); });

    const tests = [
        // Browser domain — handled locally by svc
        { id: 1,  method: 'Browser.getVersion',           params: {} },
        { id: 2,  method: 'Browser.getBrowserCommandLine', params: {} },
        // Schema — handled locally
        { id: 3,  method: 'Schema.getDomains',            params: {} },
        // Target domain — handled locally
        { id: 4,  method: 'Target.getTargets',             params: {} },
        // Page domain — needs backend (DirectCDPBackend → Chrome tab WS)
        { id: 5,  method: 'Page.getFrameTree',             params: {} },
        // Runtime domain — needs backend
        { id: 6,  method: 'Runtime.evaluate',              params: { expression: '1+1' } },
        // DOM domain
        { id: 7,  method: 'DOM.getDocument',               params: {} },
        // Network domain
        { id: 8,  method: 'Network.enable',                params: {} },
        // Input domain
        { id: 9,  method: 'Input.enable',                  params: {} },
        // Log domain
        { id: 10, method: 'Log.enable',                    params: {} },
    ];

    for (const t of tests) {
        try {
            const r = await rpc(ws, t.id, t.method, t.params);
            if (r.error) {
                console.log(\`FAIL \${t.method}: [\${r.error.code}] \${r.error.message}\`);
            } else {
                const preview = JSON.stringify(r.result || r).slice(0, 80);
                console.log(\`OK   \${t.method}: \${preview}\`);
            }
        } catch (e) {
            console.log(\`ERR  \${t.method}: \${e.message}\`);
        }
    }

    ws.close();
    process.exit(0);
}
run().catch(e => { console.error(e.message); process.exit(1); });
EOF

echo ""
echo "=== [5] SVC log ==="
cat "$LOG" | grep -v DEBUG | tail -20