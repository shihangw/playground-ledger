import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'https://ledger-api-926120652178.us-central1.run.app';

export const options = {
  scenarios: {
    stress: {
      executor: 'constant-vus',
      vus: parseInt(__ENV.VUS || '300'),
      duration: __ENV.DURATION || '20s',
    },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)'],
};

export default function () {
  const res = http.get(BASE_URL + '/health');
  check(res, { 'status 200': (r) => r.status === 200 });
}
