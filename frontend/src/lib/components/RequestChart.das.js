const recent_requests = Array.from({ length: 20 }, (_, index) => {
  const start = 360 + index * 10;
  /** @param {number} minutes */
  const time = (minutes) =>
    `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
  return {
    time: `${time(start)}-${time(start + 10)}`,
    success: [26, 32, 19, 37, 42, 61, 34, 52, 44, 56][index % 10],
    failed: index % 6 === 0 ? 3 : 0
  };
});
export default {
  name: 'Request activity',
  file: './RequestChart.svelte',
  description:
    'Actual shadcn-svelte chart primitives with LayerChart. Inspect each server-local interval with the pointer, or expand the complete accessible data table.',
  examples: [
    { title: 'Recent requests', input: { files: [{ name: 'demo.json', recent_requests }] } },
    { title: 'Empty history', input: { files: [] } },
    {
      title: 'Large request counts',
      input: {
        files: [
          {
            name: 'busy.json',
            recent_requests: recent_requests.map((bucket) => ({
              ...bucket,
              success: bucket.success * 1_000_000,
              failed: bucket.failed * 100_000
            }))
          }
        ]
      }
    },
    {
      title: 'Daylight-saving fallback',
      input: {
        files: [
          {
            name: 'dst.json',
            recent_requests: [
              { time: '02:00-02:10', timestamp: 1792886400, success: 12, failed: 2 },
              { time: '02:00-02:10', timestamp: 1792890000, success: 8, failed: 1 }
            ]
          }
        ]
      }
    },
    {
      title: 'Zero activity',
      input: {
        files: [
          {
            name: 'demo.json',
            recent_requests: recent_requests.map((bucket) => ({ ...bucket, success: 0, failed: 0 }))
          }
        ]
      }
    },
    {
      title: 'Failures',
      input: {
        files: [
          {
            name: 'demo.json',
            recent_requests: recent_requests.map((bucket) => ({
              ...bucket,
              success: 0,
              failed: bucket.success
            }))
          }
        ]
      }
    },
    {
      title: 'Midnight rollover',
      input: {
        files: [
          {
            name: 'demo.json',
            recent_requests: [
              { time: '23:50-00:00', success: 12, failed: 2 },
              { time: '00:00-00:10', success: 23, failed: 0 }
            ]
          }
        ]
      }
    }
  ]
};
