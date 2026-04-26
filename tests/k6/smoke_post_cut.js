import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';

const BASE = __ENV.API_URL || 'https://opportunities.stawi.org';

const queries = [
    'engineer', 'designer', 'sales', 'remote', 'kenya',
    'python', 'go', 'product manager', 'data scientist',
    'marketing', 'customer success', 'devops', 'intern',
    'senior', 'junior', 'lead', 'manager', 'analyst',
    'nurse', 'teacher',
];

const searchLatency = new Trend('search_latency');
const detailLatency = new Trend('detail_latency');

export const options = {
    scenarios: {
        search: {
            executor: 'constant-vus',
            vus: 5, duration: '2m',
            exec: 'smokeSearch',
        },
        detail: {
            executor: 'constant-vus',
            vus: 2, duration: '2m',
            exec: 'smokeDetail',
            startTime: '30s',
        },
    },
    thresholds: {
        'search_latency': ['p(95) < 500'],
        'detail_latency': ['p(95) < 300'],
        'http_req_failed': ['rate < 0.01'],
    },
};

export function smokeSearch() {
    const q = queries[Math.floor(Math.random() * queries.length)];
    const res = http.get(`${BASE}/api/v2/search?q=${encodeURIComponent(q)}&limit=20`);
    check(res, {
        '200 OK': (r) => r.status === 200,
        'has results': (r) => {
            try { return JSON.parse(r.body).results.length > 0; } catch (e) { return false; }
        },
        'has facets': (r) => {
            try { return Object.keys(JSON.parse(r.body).facets || {}).length > 0; } catch (e) { return false; }
        },
    });
    searchLatency.add(res.timings.duration);
    sleep(0.2);
}

export function smokeDetail() {
    const seedSlugs = (__ENV.DETAIL_SLUGS || '').split(',').filter(Boolean);
    let slugs = seedSlugs;
    if (slugs.length === 0) {
        const res = http.get(`${BASE}/api/v2/jobs/latest?limit=5`);
        if (res.status === 200) {
            try {
                const results = JSON.parse(res.body).results || [];
                results.forEach((r) => slugs.push(r.slug));
            } catch (e) {}
        }
    }
    if (slugs.length === 0) return;
    const slug = slugs[Math.floor(Math.random() * slugs.length)];
    const res = http.get(`${BASE}/api/v2/jobs/${slug}`);
    check(res, {
        '200 OK': (r) => r.status === 200,
        'has title': (r) => {
            try { return !!JSON.parse(r.body).title; } catch (e) { return false; }
        },
    });
    detailLatency.add(res.timings.duration);
    sleep(0.5);
}
