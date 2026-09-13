import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { useEvolutionRun, isEvolutionJobTerminal, EVOLUTION_ACTIVE_POLL_MS } from './useEvolutionRun';
import type { EvolutionJob, EvalMeta, SkillCandidate } from '../types';

const job: EvolutionJob = {
  id: 'job-1',
  skill_slug: 'stability-protocol',
  profile: 'opencode',
  status: 'running',
  iteration: 2,
  max_iterations: 12,
  run_deadline_at: '2026-09-13T12:00:00Z',
  eval_hash: 'abc123',
  created_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const evalMeta: EvalMeta = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  filename: 'cases.json',
  split_mode: 'auto',
  train_count: 30,
  val_count: 10,
  eval_hash: 'abc123',
  baseline_s0: 0.4,
  scorer_version: 'v0',
  dry_run: [],
  estimate: { estimated_val_cases: 10, estimated_cost_usd: 0, budget_enforced: false, note: '' },
  uploaded_at: '2026-09-13T10:00:00Z',
};

const candidate: SkillCandidate = {
  id: 'cand-1',
  parent_slug: 'stability-protocol',
  parent_version: 1,
  diff: '--- a/s\n+++ b/s',
  pattern_slugs: ['p1'],
  proposer: 'harness',
  created_at: '2026-09-13T10:00:00Z',
  status: 'pending',
};

function jsonResponse(body: unknown, ok = true) {
  return { ok, status: ok ? 200 : 500, json: async () => body } as Response;
}

const loopBody = {
  loop: { job_id: 'job-1', skill_slug: 'stability-protocol', status: 'running', current_iteration: 2, current_phase: 'gating', phase_status: '', started_at: '2026-09-13T10:00:00Z', updated_at: '2026-09-13T10:00:00Z' },
  iterations: [{ job_id: 'job-1', skill_slug: 'stability-protocol', iteration: 1, started_at: '2026-09-13T10:00:00Z', phases: [], val_score: 0.5, r_best: 0.5, outcome: 'accepted', candidate_id: 'cand-1' }],
  plateau_count: 0,
  max_iterations: 12,
  plateau_limit: 3,
};

describe('isEvolutionJobTerminal', () => {
  it('recognizes the four terminal statuses', () => {
    for (const s of ['complete', 'failed', 'timeout', 'cancelled']) expect(isEvolutionJobTerminal(s)).toBe(true);
    for (const s of ['queued', 'claimed', 'running', 'paused', undefined]) expect(isEvolutionJobTerminal(s)).toBe(false);
  });
});

describe('useEvolutionRun', () => {
  beforeEach(() => {
    // shouldAdvanceTime keeps waitFor's internal interval alive while the
    // polling interval itself stays controllable.
    vi.useFakeTimers({ shouldAdvanceTime: true, advanceTimeDelta: 25 });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('is idle without a job id', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const { result } = renderHook(() => useEvolutionRun(null));
    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
    expect(result.current.job).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('polls job, loop, eval, and candidate data for a live run', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/api/evolution/jobs/job-1')) return Promise.resolve(jsonResponse(job));
      if (url.endsWith('/loop')) return Promise.resolve(jsonResponse(loopBody));
      if (url.endsWith('/eval')) return Promise.resolve(jsonResponse(evalMeta));
      if (url.includes('/api/evolution/candidates/')) return Promise.resolve(jsonResponse(candidate));
      return Promise.resolve(jsonResponse({}, false));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useEvolutionRun('job-1'));
    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
    await waitFor(() => {
      expect(result.current.evalMeta?.train_count).toBe(30);
      expect(result.current.candidates['cand-1']?.diff).toBe('--- a/s\n+++ b/s');
    });
    expect(result.current.iterations).toHaveLength(1);
    expect(result.current.maxIterations).toBe(12);
    expect(result.current.plateauLimit).toBe(3);
    expect(result.current.loop?.current_phase).toBe('gating');
  });

  it('keeps polling while the run is active and stops when it turns terminal', async () => {
    let status = 'running';
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/api/evolution/jobs/job-1')) return Promise.resolve(jsonResponse({ ...job, status }));
      if (url.endsWith('/loop')) return Promise.resolve(jsonResponse(loopBody));
      return Promise.resolve(jsonResponse({}, false));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useEvolutionRun('job-1'));
    await waitFor(() => expect(result.current.job).not.toBeNull());

    const before = fetchMock.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(EVOLUTION_ACTIVE_POLL_MS + 100);
    });
    expect(fetchMock.mock.calls.length).toBeGreaterThan(before);

    // The run completes: the next poll sees it and the interval stops firing.
    status = 'complete';
    await act(async () => {
      await vi.advanceTimersByTimeAsync(EVOLUTION_ACTIVE_POLL_MS + 100);
    });
    await waitFor(() => expect(result.current.job?.status).toBe('complete'));
    const after = fetchMock.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(EVOLUTION_ACTIVE_POLL_MS * 3);
    });
    expect(fetchMock.mock.calls.length).toBe(after);
  });

  it('keeps the last snapshot when a poll fails, and surfaces the error', async () => {
    let fail = false;
    const fetchMock = vi.fn((url: string) => {
      if (fail) return Promise.resolve(jsonResponse({}, false));
      if (url.endsWith('/api/evolution/jobs/job-1')) return Promise.resolve(jsonResponse(job));
      if (url.endsWith('/loop')) return Promise.resolve(jsonResponse(loopBody));
      return Promise.resolve(jsonResponse({}, false));
    });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useEvolutionRun('job-1'));
    await waitFor(() => expect(result.current.job).not.toBeNull());

    fail = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(EVOLUTION_ACTIVE_POLL_MS + 100);
    });
    await waitFor(() => expect(result.current.error).toBeTruthy());
    // The last good snapshot survives the failed poll.
    expect(result.current.job?.id).toBe('job-1');
    expect(result.current.iterations).toHaveLength(1);
  });

  it('reports a missing job through error, not a thrown rejection', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({}, false)));
    vi.stubGlobal('fetch', fetchMock);
    const { result } = renderHook(() => useEvolutionRun('gone'));
    await waitFor(() => {
      expect(result.current.error).toContain('not found');
    });
    expect(result.current.loading).toBe(false);
  });
});
