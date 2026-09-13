// ShortLink 跳转链路压测：GET /:code （成功码 301，k6 默认不跟随重定向）
//
// 常用：
//   MODE=smoke     k6 run scripts/loadtest/k6/redirect.js
//   MODE=baseline RATE=2000 DURATION=60s k6 run scripts/loadtest/k6/redirect.js
//   MODE=ramp       k6 run scripts/loadtest/k6/redirect.js
//   MODE=soak  RATE=1000 k6 run scripts/loadtest/k6/redirect.js
//
// 环境变量：
//   BASE_URL   目标地址，默认 http://localhost:8080
//   CODES      逗号分隔的短码列表；不传则自动创建 SEED_COUNT 条种子数据
//   SEED_COUNT 自动造种子的条数，默认 20
//   MODE       smoke|baseline|ramp|soak|spike|fixed，默认 smoke
//   RATE       目标到达率 req/s
//   DURATION   持续时间（baseline/fixed 用）
//   VUS        预分配 VU
//   P99_MS     阈值：p99 上限，默认 500
//   SUMMARY_PATH  若设置，把原始 JSON 摘要写到此路径

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import {
  buildScenarios, buildThresholds, makeSummary, envStr, envNum, pickCode, randomString,
} from './lib/common.js';

const BASE = envStr('BASE_URL', 'http://localhost:8080').replace(/\/+$/, '');
const MODE = envStr('MODE', 'smoke');
const RATE = envNum('RATE', 1000);
const DURATION = envStr('DURATION', '60s');
const VUS = envNum('VUS', 0);
const SEED_COUNT = envNum('SEED_COUNT', 20);
const P99_MS = envNum('P99_MS', 500);

const notFound = new Rate('redirect_not_found');
const serverError = new Rate('redirect_5xx');
const redirectLatency = new Trend('redirect_latency_ms', true);

export const options = {
  scenarios: buildScenarios('redirect', {
    rate: RATE,
    duration: DURATION,
    vus: VUS > 0 ? VUS : Math.max(50, Math.ceil(RATE * 0.5)),
  }),
  thresholds: Object.assign(buildThresholds(P99_MS), {
    redirect_not_found: ['rate<0.01'],
    redirect_5xx: ['rate<0.01'],
  }),
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
  // 注意：这里不要开 discardResponseBodies —— 它是全局的，会让 setup() 造种子时
  // 拿不到响应体而解析不出 short_code。跳转响应本身没有 body，丢弃也没收益。
};

// setup 只执行一次：没有传 CODES 时自动造种子数据，避免压测打到不存在的短码
export function setup() {
  let codes = envStr('CODES', '').split(',').map((s) => s.trim()).filter(Boolean);
  if (codes.length > 0) return { codes };

  const created = [];
  for (let i = 0; i < SEED_COUNT; i++) {
    const res = http.post(
      `${BASE}/api/short-links`,
      JSON.stringify({ origin_url: `https://example.com/seed-${randomString(16)}` }),
      { headers: { 'Content-Type': 'application/json' }, tags: { name: 'seed-create' } },
    );
    if (res.status >= 200 && res.status < 300) {
      try {
        const body = JSON.parse(res.body);
        if (body.short_code) created.push(body.short_code);
      } catch (e) {
        // 忽略解析失败，继续造种子
      }
    }
  }
  if (created.length === 0) {
    throw new Error(`造种子失败：${BASE} 不可用，或创建接口返回异常（先跑 MODE=smoke 检查链路）`);
  }
  return { codes: created };
}

export default function (data) {
  const code = pickCode(data.codes, __VU, __ITER);
  const res = http.get(`${BASE}/${code}`, {
    redirects: 0,
    tags: { name: 'redirect' },
  });

  redirectLatency.add(res.timings.duration);
  notFound.add(res.status === 404);
  serverError.add(res.status >= 500);

  check(res, {
    'status is 301': (r) => r.status === 301,
    'has Location': (r) => !!(r.headers['Location'] || r.headers['location']),
  });
}

export const handleSummary = makeSummary({
  scenario: MODE,
  target: `${BASE}/:code`,
  rate: RATE,
  duration: DURATION,
});
