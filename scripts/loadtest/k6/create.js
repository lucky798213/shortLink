// ShortLink 创建链路压测：POST /api/short-links （成功码 201 / 2xx）
//
// 与 wrk 版（scripts/wrk/create.lua）的区别：k6 用开模型（恒定到达率）施压，
// 不会出现「线程被响应时间拖住 → 实际 QPS 掉下来但延迟看着还行」的 coordinated omission。
//
// 常用：
//   MODE=smoke    k6 run scripts/loadtest/k6/create.js
//   MODE=baseline RATE=300 DURATION=60s k6 run scripts/loadtest/k6/create.js
//
// 环境变量：同 redirect.js（BASE_URL / MODE / RATE / DURATION / VUS / P99_MS / SUMMARY_PATH）
//   ORIGIN_PREFIX  生成 origin_url 的前缀，默认 https://example.com/lt

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Counter, Trend } from 'k6/metrics';
import { buildScenarios, buildThresholds, makeSummary, envStr, envNum, randomString } from './lib/common.js';

const BASE = envStr('BASE_URL', 'http://localhost:8080').replace(/\/+$/, '');
const MODE = envStr('MODE', 'smoke');
const RATE = envNum('RATE', 300);
const DURATION = envStr('DURATION', '60s');
const VUS = envNum('VUS', 0);
const P99_MS = envNum('P99_MS', 800);
const ORIGIN_PREFIX = envStr('ORIGIN_PREFIX', 'https://example.com/lt');

const created = new Counter('created_total');
const conflict = new Rate('create_conflict');
const serverError = new Rate('create_5xx');
const createLatency = new Trend('create_latency_ms', true);

export const options = {
  scenarios: buildScenarios('create', {
    rate: RATE,
    duration: DURATION,
    vus: VUS > 0 ? VUS : Math.max(50, Math.ceil(RATE * 1.5)),
  }),
  thresholds: Object.assign(buildThresholds(P99_MS), {
    create_conflict: ['rate<0.01'],
    create_5xx: ['rate<0.01'],
  }),
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
};

export default function () {
  const payload = JSON.stringify({
    origin_url: `${ORIGIN_PREFIX}/${Date.now()}-${__VU}-${__ITER}-${randomString(12)}`,
  });

  const res = http.post(`${BASE}/api/short-links`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'create' },
  });

  createLatency.add(res.timings.duration);
  if (res.status >= 200 && res.status < 300) created.add(1);
  conflict.add(res.status === 409);
  serverError.add(res.status >= 500);

  check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
    'has short_code': (r) => {
      if (r.status < 200 || r.status >= 300) return false;
      return /"short_code"\s*:\s*"[^"]+"/.test(r.body || '');
    },
  });
}

export const handleSummary = makeSummary({
  scenario: MODE,
  target: `${BASE}/api/short-links`,
  rate: RATE,
  duration: DURATION,
});
