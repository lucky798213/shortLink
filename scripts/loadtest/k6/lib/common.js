// ShortLink k6 压测公共模块：环境变量解析、场景构造、自定义摘要输出。
// k6 脚本通过相对路径 import 本文件，无需网络（不依赖 jslib 外链）。

export function envStr(name, def) {
  const v = __ENV[name];
  return v === undefined || v === '' ? def : v;
}

export function envNum(name, def) {
  const v = __ENV[name];
  if (v === undefined || v === '') return def;
  const n = Number(v);
  return Number.isFinite(n) ? n : def;
}

const CHARSET = 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ';

export function randomString(n) {
  let s = '';
  for (let i = 0; i < n; i++) s += CHARSET[Math.floor(Math.random() * CHARSET.length)];
  return s;
}

export function pickCode(codes, vu, iter) {
  return codes[(vu * 7919 + iter) % codes.length];
}

// 解析 "0:30s,500:1m,1000:2m" -> [{target:0,duration:'30s'}, ...]
function parseStages(spec) {
  if (!spec) return null;
  return spec.split(',').map((pair) => {
    const [target, duration] = pair.split(':');
    return { target: Number(target.trim()), duration: duration.trim() };
  });
}

/**
 * 构造场景。MODE 决定压测形态：
 *   smoke    2 VU 跑 10s，确认链路通
 *   baseline 恒定到达率（开模型），测稳定吞吐
 *   ramp     阶梯加压，找拐点
 *   soak     长时间稳定性（默认 30m）
 *   spike    尖峰冲击，测削峰与恢复
 *   fixed    恒定 VU 数（闭模型，仅用于对比工具差异）
 */
export function buildScenarios(kind, opts) {
  const {
    rate = 1000,
    duration = '60s',
    vus = Math.max(50, Math.ceil(rate * 0.5)),
    execName = 'default',
  } = opts || {};
  // 阶梯/尖峰用到的阶梯序列，可用 RAMP_STAGES 覆盖，例如：
  //   RAMP_STAGES=0:30s,500:1m,1000:2m
  const rampStages = (opts && opts.rampStages) || envStr('RAMP_STAGES', '0:30s,500:1m,1000:2m,2000:2m,3000:3m');

  const MODE = envStr('MODE', 'smoke');
  const tagKind = { exec: execName };

  switch (MODE) {
    case 'baseline':
      return {
        [kind]: {
          executor: 'constant-arrival-rate',
          rate,
          timeUnit: '1s',
          duration,
          preAllocatedVUs: vus,
          maxVUs: vus * 4,
          ...tagKind,
        },
      };
    case 'ramp':
      return {
        [kind]: {
          executor: 'ramping-arrival-rate',
          startRate: rate,
          timeUnit: '1s',
          stages: parseStages(rampStages),
          preAllocatedVUs: vus,
          maxVUs: vus * 8,
          ...tagKind,
        },
      };
    case 'soak':
      return {
        [kind]: {
          executor: 'constant-arrival-rate',
          rate,
          timeUnit: '1s',
          duration: envStr('DURATION', '30m'),
          preAllocatedVUs: vus,
          maxVUs: vus * 4,
          ...tagKind,
        },
      };
    case 'spike':
      return {
        [kind]: {
          executor: 'ramping-arrival-rate',
          startRate: Math.max(10, Math.round(rate * 0.1)),
          timeUnit: '1s',
          stages: [
            { target: Math.max(10, Math.round(rate * 0.1)), duration: '30s' },
            { target: rate, duration: '20s' },   // 突增
            { target: rate, duration: '60s' },   // 顶住
            { target: Math.max(10, Math.round(rate * 0.1)), duration: '30s' }, // 回落看恢复
          ],
          preAllocatedVUs: vus,
          maxVUs: vus * 8,
          ...tagKind,
        },
      };
    case 'fixed':
      return {
        [kind]: {
          executor: 'constant-vus',
          vus,
          duration,
          ...tagKind,
        },
      };
    case 'smoke':
    default:
      return {
        [kind]: {
          executor: 'constant-vus',
          vus: 2,
          duration: '10s',
          ...tagKind,
        },
      };
  }
}

export function buildThresholds(p99ms) {
  return {
    // 跳转链路成功码是 301，k6 默认把 200-399 视为 expected，故失败率只统计 4xx/5xx 与超时
    http_req_failed: ['rate<0.01'],
    http_req_duration: [`p(99)<${p99ms}`],
  };
}

/**
 * 自定义摘要：输出到 stdout，并在 SUMMARY_PATH 指定时落一份 JSON 原始数据。
 * 不依赖 k6 版本差异较大的 --summary-export 参数。
 */
export function makeSummary(meta) {
  return function handleSummary(data) {
    const m = data.metrics || {};
    const g = (name, key) => {
      const metric = m[name];
      if (!metric) return '-';
      const v = metric.values ? metric.values[key] : undefined;
      return v === undefined ? '-' : v;
    };
    // Gauge / Counter 在不同 k6 版本里的 values key 不一致，做个兜底
    const gAny = (name, keys) => {
      for (const k of keys) {
        const v = g(name, k);
        if (v !== '-') return v;
      }
      return '-';
    };
    const fixed = (v, n = 2) => (typeof v === 'number' ? v.toFixed(n) : v);

    const lines = [];
    lines.push('');
    lines.push('================ ShortLink Load Test Summary ================');
    Object.keys(meta).forEach((k) => lines.push(`  ${k.padEnd(14)}: ${meta[k]}`));
    lines.push('------------------------------------------------------------');
    lines.push(`  http_reqs      : ${g('http_reqs', 'count')}`);
    lines.push(`  throughput     : ${fixed(g('http_reqs', 'rate'))} req/s`);
    lines.push(`  failed rate    : ${fixed((g('http_req_failed', 'rate') || 0) * 100, 3)} %`);
    lines.push(`  duration avg   : ${fixed(g('http_req_duration', 'avg'))} ms`);
    lines.push(`  duration p50   : ${fixed(gAny('http_req_duration', ['p(50)', 'med']))} ms`);
    lines.push(`  duration p90   : ${fixed(gAny('http_req_duration', ['p(90)']))} ms`);
    lines.push(`  duration p99   : ${fixed(gAny('http_req_duration', ['p(99)']))} ms`);
    const p999 = gAny('http_req_duration', ['p(99.9)']);
    if (p999 !== '-') lines.push(`  duration p99.9 : ${fixed(p999)} ms`);
    lines.push(`  max VUs        : ${gAny('vus_max', ['max', 'value'])}`);
    const dropped = g('dropped_iterations', 'count');
    lines.push(`  dropped iters  : ${dropped === '-' ? 0 : dropped}   (>0 说明预分配 VU 不够，实际到达率没打满)`);
    const checkRate = g('checks', 'rate');
    if (typeof checkRate === 'number') {
      lines.push(`  checks         : passed ${fixed(checkRate * 100, 3)} % / failed ${fixed((1 - checkRate) * 100, 3)} %`);
    }
    lines.push('============================================================');
    lines.push('');

    const out = { stdout: lines.join('\n') };
    const path = envStr('SUMMARY_PATH', '');
    if (path) out[path] = JSON.stringify(data, null, 2);
    return out;
  };
}
