// ShortLink 读写混合压测：跳转 : 创建 = (1-WRITE_RATIO) : WRITE_RATIO，默认 9:1
//
// 这是最接近线上真实流量的形态：读多写少，可以观察写路径（号段 ID + 批量落库）
// 对读路径 p99 的影响，以及缓存预热后写放大是否拖垮 Redis/MySQL。
//
// 常用：
//   MODE=baseline RATE=2000 WRITE_RATIO=0.1 DURATION=120s k6 run scripts/loadtest/k6/mixed.js
//
// 说明：MODE=ramp/spike 时两个场景共用同一套阶梯，写链路会被一起放大，
//       如需单独考察写入极限，请用 create.js 单独压。
//
// 环境变量：BASE_URL / MODE / RATE（读的到达率）/ WRITE_RATIO / DURATION / VUS / P99_MS / SEED_COUNT

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Counter } from 'k6/metrics';
import {
  buildScenarios, makeSummary, envStr, envNum, pickCode, randomString,
} from './lib/common.js';

const BASE = envStr('BASE_URL', 'http://localhost:8080').replace(/\/+$/, '');
const MODE = envStr('MODE', 'smoke');
const READ_RATE = envNum('RATE', 2000);
const WRITE_RATIO = envNum('WRITE_RATIO', 0.1);
const DURATION = envStr('DURATION', '120s');
const VUS = envNum('VUS', 0);
const SEED_COUNT = envNum('SEED_COUNT', 20);
const P99_MS = envNum('P99_MS', 500);

const WRITE_RATE = Math.max(1, Math.round(READ_RATE * WRITE_RATIO));
const readVUs = VUS > 0 ? VUS : Math.max(50, Math.ceil(READ_RATE * 0.5));
const writeVUs = VUS > 0 ? VUS : Math.max(20, Math.ceil(WRITE_RATE * 1.5));

const created = new Counter('created_total');
const notFound = new Rate('redirect_not_found');
const serverError = new Rate('server_5xx');

export const options = {
  scenarios: Object.assign(
    buildScenarios('redirect_read', { rate: READ_RATE, duration: DURATION, vus: readVUs, execName: 'redirectFn' }),
    buildScenarios('create_write', { rate: WRITE_RATE, duration: DURATION, vus: writeVUs, execName: 'createFn' }),
  ),
  thresholds: {
    // 按业务标签分别设阈值，避免写路径拖累读路径的判定
    'http_req_duration{name:redirect}': [`p(99)<${P99_MS}`],
    'http_req_duration{name:create}': [`p(99)<${P99_MS * 2}`],
    'http_req_failed': ['rate<0.01'],
    redirect_not_found: ['rate<0.01'],
    server_5xx: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
};

export function setup() {
  let codes = envStr('CODES', '').split(',').map((s) => s.trim()).filter(Boolean);
  if (codes.length > 0) return { codes };

  const createdCodes = [];
  for (let i = 0; i < SEED_COUNT; i++) {
    const res = http.post(
      `${BASE}/api/short-links`,
      JSON.stringify({ origin_url: `https://example.com/seed-${randomString(16)}` }),
      { headers: { 'Content-Type': 'application/json' }, tags: { name: 'seed-create' } },
    );
    if (res.status >= 200 && res.status < 300) {
      try {
        const body = JSON.parse(res.body);
        if (body.short_code) createdCodes.push(body.short_code);
      } catch (e) { /* 忽略 */ }
    }
  }
  if (createdCodes.length === 0) {
    throw new Error(`造种子失败：${BASE} 不可用或创建接口异常`);
  }
  return { codes: createdCodes };
}

export function redirectFn(data) {
  const code = pickCode(data.codes, __VU, __ITER);
  const res = http.get(`${BASE}/${code}`, { redirects: 0, tags: { name: 'redirect' } });
  notFound.add(res.status === 404);
  serverError.add(res.status >= 500);
  check(res, { 'redirect 301': (r) => r.status === 301 });
}

export function createFn() {
  const payload = JSON.stringify({
    origin_url: `https://example.com/lt/${Date.now()}-${__VU}-${__ITER}-${randomString(12)}`,
  });
  const res = http.post(`${BASE}/api/short-links`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'create' },
  });
  if (res.status >= 200 && res.status < 300) created.add(1);
  serverError.add(res.status >= 500);
  check(res, { 'create 2xx': (r) => r.status >= 200 && r.status < 300 });
}

export const handleSummary = makeSummary({
  scenario: `${MODE} (mixed)`,
  target: BASE,
  readRate: READ_RATE,
  writeRate: WRITE_RATE,
  duration: DURATION,
});
