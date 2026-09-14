import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, act, within, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SkillTrainWizard } from './SkillTrainWizard';
import { pickFollowedJob } from './wizardChips';
import type { EvolutionJob, EvalMeta, SkillCandidate, SkillRegistryEntry } from '../types';

const skill: SkillRegistryEntry = {
  name: 'stability-protocol',
  title: 'Stability Protocol',
  description: 'The standing procedure.',
  version: 3,
  tags: ['ai-agent-skill'],
  trained_state: { state: 'untrained', current_version: 3 },
};

/** Registry row for a skill whose marker is live — the rollback control needs one. */
const trainedSkill: SkillRegistryEntry = {
  ...skill,
  trained_state: {
    state: 'trained',
    trained_at: '2026-09-13T10:05:00Z',
    trained_version: 3,
    trained_parent_version: 2,
    trained_candidate: 'cand-1',
    trained_val_score: 0.55,
    trained_eval_hash: 'a'.repeat(64),
    current_version: 3,
    current_eval_hash: 'a'.repeat(64),
  },
};

const job: EvolutionJob = {
  id: 'job-1',
  skill_slug: 'stability-protocol',
  profile: 'opencode',
  status: 'running',
  iteration: 2,
  max_iterations: 12,
  plateau_limit: 3,
  eval_hash: 'b'.repeat(64),
  run_deadline_at: '2026-09-13T12:00:00Z',
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
  eval_hash: 'a'.repeat(64),
  baseline_s0: 0.4,
  scorer_version: 'v0',
  dry_run: [],
  estimate: { estimated_val_cases: 10, estimated_cost_usd: 0, budget_enforced: false, note: '' },
  uploaded_at: '2026-09-13T10:02:00Z',
};

const candidate: SkillCandidate = {
  id: 'cand-1',
  parent_slug: 'stability-protocol',
  parent_version: 3,
  diff: '--- a/s\n+++ b/s\n+new',
  pattern_slugs: ['pattern-a'],
  proposer: 'harness',
  created_at: '2026-09-13T10:03:00Z',
  status: 'pending',
};

const doneJob: EvolutionJob = {
  ...job,
  status: 'complete',
  loop_outcome: 'completed',
  completed_at: '2026-09-13T10:20:00Z',
};

const auditBody = {
  slug: 'stability-protocol',
  r_best: 0.55,
  records: [
    {
      candidate_id: 'cand-1',
      skill_slug: 'stability-protocol',
      parent_version: 3,
      content_hash: 'h1',
      validation_score: 0.55,
      r_best_before: 0.4,
      r_best_after: 0.55,
      outcome: 'accepted',
      decider: 'gate',
      scorer_version: 'v0',
      timestamp: '2026-09-13T10:05:00Z',
    },
  ],
};

const rolledBackState = {
  slug: 'stability-protocol',
  title: 'Stability Protocol',
  state: 'stale',
  revoked_at: '2026-09-13T11:00:00Z',
  revoked_reason: 'operator rollback: wrong direction',
  trained_version: 4,
  trained_parent_version: 3,
  trained_val_score: 0.55,
  current_version: 3,
};

const reportView = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  status: 'complete',
  loop_outcome: 'completed',
  terminal: true,
  report: {
    slug: 'trained-skill-result-stability-protocol-run-job-1',
    title: 'Trained Skill Result: Stability Protocol (run job-1)',
    url: '/articles/trained-skill-result-stability-protocol-run-job-1',
    created_at: '2026-09-13T10:20:00Z',
  },
  trained_state: { state: 'trained', trained_version: 4, trained_val_score: 0.55, current_version: 4 },
};

const loopBody = {
  loop: {
    job_id: 'job-1',
    skill_slug: 'stability-protocol',
    status: 'running',
    current_iteration: 2,
    current_phase: 'inference',
    phase_status: 'running',
    note: 'reading the wiki',
    started_at: '2026-09-13T10:00:00Z',
    updated_at: '2026-09-13T10:00:00Z',
  },
  iterations: [
    {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      iteration: 1,
      started_at: '2026-09-13T10:00:00Z',
      completed_at: '2026-09-13T10:05:00Z',
      phases: [{ phase: 'gating', entered_at: '2026-09-13T10:04:00Z', exited_at: '2026-09-13T10:05:00Z', complete: true, note: '' }],
      candidate_id: 'cand-1',
      pattern_slugs: ['pattern-a'],
      val_score: 0.5,
      r_best: 0.4,
      outcome: 'accepted',
      result_version: 4,
    },
  ],
  plateau_count: 0,
  max_iterations: 12,
  plateau_limit: 3,
};

const doneLoopBody = {
  loop: {
    job_id: 'job-1',
    skill_slug: 'stability-protocol',
    status: 'terminal',
    current_iteration: 2,
    current_phase: 'gating',
    phase_status: '',
    started_at: '2026-09-13T10:00:00Z',
    updated_at: '2026-09-13T10:20:00Z',
    outcome: 'completed',
    outcome_reason: 'max iterations reached',
  },
  iterations: loopBody.iterations,
  plateau_count: 0,
  max_iterations: 12,
  plateau_limit: 3,
};

function jsonResponse(body: unknown, ok = true) {
  return { ok, status: ok ? 200 : 500, json: async () => body } as Response;
}

function setupFetch(overrides: Partial<Record<string, unknown>> = {}) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars -- init kept so the mock calls record POST metadata for assertions
  return vi.fn((url: string, _init?: RequestInit) => {
    if (url.includes('/api/evolution/jobs?skill=')) {
      return Promise.resolve(jsonResponse(overrides['jobs'] ?? [job]));
    }
    if (url.endsWith('/api/evolution/jobs/job-1/loop')) {
      return Promise.resolve(jsonResponse(overrides['loop'] ?? loopBody));
    }
    if (url.endsWith('/api/evolution/jobs/job-1/eval')) {
      return Promise.resolve(jsonResponse(overrides['eval'] ?? evalMeta));
    }
    if (url.includes('/api/evolution/candidates/')) {
      return Promise.resolve(jsonResponse(overrides['candidate'] ?? candidate));
    }
    if (url.endsWith('/api/skills/stability-protocol/audit')) {
      return Promise.resolve(jsonResponse(overrides['audit'] ?? auditBody));
    }
    if (url.endsWith('/api/skills/stability-protocol/rollback')) {
      return Promise.resolve(jsonResponse(overrides['rollback'] ?? rolledBackState));
    }
    if (url.endsWith('/api/evolution/jobs/job-1/report')) {
      return Promise.resolve(jsonResponse(overrides['report'] ?? reportView));
    }
    if (url.endsWith('/pause') || url.endsWith('/abort') || url.endsWith('/approve')) {
      return Promise.resolve(jsonResponse(overrides['control'] ?? { ...job, status: 'paused' }));
    }
    if (url.endsWith('/api/evolution/jobs/job-1')) {
      return Promise.resolve(jsonResponse(overrides['job'] ?? job));
    }
    return Promise.resolve(jsonResponse({}, false));
  });
}

describe('pickFollowedJob', () => {
  it('prefers the newest active job over terminal history, else falls back to the newest record', () => {
    const terminal: EvolutionJob = { ...job, id: 'old', status: 'complete', created_at: '2026-09-12T09:00:00Z' };
    expect(pickFollowedJob([terminal])?.id).toBe('old');
    expect(pickFollowedJob([terminal, job])?.id).toBe('job-1');
    expect(pickFollowedJob([])).toBeNull();
  });
});

describe('SkillTrainWizard', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true, advanceTimeDelta: 25 });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const baseProps = {
    slug: 'stability-protocol',
    skills: [skill],
    onSkillsRefresh: vi.fn().mockResolvedValue(undefined),
    onNavigate: vi.fn(),
    onAlert: vi.fn(),
  };

  it('auto-jumps to the live view for a running run and renders the server state', async () => {
    vi.stubGlobal('fetch', setupFetch());
    const { container } = render(<SkillTrainWizard {...baseProps} />);

    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    // Step 4 was auto-selected, the stepper highlights Evolve, and the feed
    // carries the real iteration record.
    expect(container.textContent).toContain('Run running');
    expect(screen.getByTestId('wizard-iteration-card-1')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-score-strip')).toBeInTheDocument();
    // The data summary from the eval metadata is reachable through the rail.
    await userEvent.click(screen.getByText('Data'));
    await waitFor(() => {
      expect(screen.getByText('Train cases')).toBeInTheDocument();
    });
  });

  it('shows the picker when no run exists and navigates between skills', async () => {
    vi.stubGlobal('fetch', setupFetch({ jobs: [] }));
    render(<SkillTrainWizard {...baseProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-picker-stability-protocol')).toBeInTheDocument();
    });
    expect(screen.getAllByText(/No run yet/).length).toBeGreaterThan(0);
  });

  it('navigates a picker selection to the other skill\'s train route exactly once', async () => {
    const secondSkill: SkillRegistryEntry = { ...skill, name: 'docker-cleanup', title: 'Docker Cleanup' };
    vi.stubGlobal('fetch', setupFetch({ jobs: [] }));
    const onNavigate = vi.fn();
    render(<SkillTrainWizard {...baseProps} skills={[skill, secondSkill]} onNavigate={onNavigate} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-picker-docker-cleanup')).toBeInTheDocument();
    });
    // onNavigate is the app router's navigateTo, which passes '/'-prefixed targets
    // through untouched — so the picker hands over the full /skills/<slug>/train
    // path. A bare slug here would land on /articles/<slug> (the wrong page), and
    // the router must never see a doubled /articles//skills/... prefix.
    await userEvent.click(screen.getByTestId('wizard-picker-docker-cleanup'));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(onNavigate).toHaveBeenCalledWith('/skills/docker-cleanup/train');
  });

  it('renders the skill-not-found panel for a slug outside the registry', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<SkillTrainWizard {...baseProps} slug="ghost-skill" />);
    await waitFor(() => {
      expect(screen.getByText(/not in the registry/i)).toBeInTheDocument();
    });
  });

  it('pauses the run through the REST endpoint and toasts the result', async () => {
    const fetchMock = setupFetch();
    vi.stubGlobal('fetch', fetchMock);
    const onAlert = vi.fn();
    render(<SkillTrainWizard {...baseProps} onAlert={onAlert} />);

    await waitFor(() => {
      expect(screen.getByTestId('wizard-pause-button')).toBeEnabled();
    });
    await userEvent.click(screen.getByTestId('wizard-pause-button'));
    await waitFor(() => {
      expect(onAlert).toHaveBeenCalledWith(
        'success',
        'Pause requested — the run parks at its next step boundary.',
      );
    });
    const posted = fetchMock.mock.calls.find(
      ([url, init]) => String(url).endsWith('/pause') && (init as RequestInit | undefined)?.method === 'POST',
    );
    expect(posted).toBeTruthy();
  });

  it('aborts with a confirm dialog and reports failures as error toasts', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true));
    const fetchMock = setupFetch();
    // Make the abort endpoint fail for the error-toast path.
    fetchMock.mockImplementation((url: string) => {
      if (String(url).endsWith('/abort')) {
        return Promise.resolve(jsonResponse({ error: 'cannot cancel evolution job: already terminal' }, false));
      }
      if (String(url).includes('/api/evolution/jobs?skill=')) return Promise.resolve(jsonResponse([job]));
      if (String(url).endsWith('/api/evolution/jobs/job-1/loop')) return Promise.resolve(jsonResponse(loopBody));
      if (String(url).endsWith('/api/evolution/jobs/job-1/eval')) return Promise.resolve(jsonResponse(evalMeta));
      if (String(url).includes('/api/evolution/candidates/')) return Promise.resolve(jsonResponse(candidate));
      return Promise.resolve(jsonResponse(job));
    });
    vi.stubGlobal('fetch', fetchMock);
    const onAlert = vi.fn();
    render(<SkillTrainWizard {...baseProps} onAlert={onAlert} />);

    await waitFor(() => {
      expect(screen.getByTestId('wizard-abort-button')).toBeEnabled();
    });
    await userEvent.click(screen.getByTestId('wizard-abort-button'));
    await waitFor(() => {
      expect(onAlert).toHaveBeenCalledWith(
        'error',
        'cannot cancel evolution job: already terminal',
      );
    });
  });

  it('keeps polling while the run is live', async () => {
    const fetchMock = setupFetch();
    vi.stubGlobal('fetch', fetchMock);
    render(<SkillTrainWizard {...baseProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    const before = fetchMock.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4500);
    });
    expect(fetchMock.mock.calls.length).toBeGreaterThan(before);
  });

  it('renders the Review step from the audit trail and the loop records', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<SkillTrainWizard {...baseProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText('Review'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-baseline-final')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-review-r-best')).toHaveTextContent('55% (R_best)');
    expect(screen.getByText('40% (S0, v0 scorer)')).toBeInTheDocument();
    // cand-1 appears in both the iteration and audit tables; scope to iterations.
    expect(within(screen.getByTestId('wizard-review-iterations')).getByText('cand-1')).toBeInTheDocument();
    expect(screen.getByText(/Patterns added: 1\./)).toBeInTheDocument();
  });

  it('rolls back from the Review step and re-renders the derived state from the response', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true));
    const onAlert = vi.fn();
    const onSkillsRefresh = vi.fn().mockResolvedValue(undefined);
    const fetchMock = setupFetch();
    vi.stubGlobal('fetch', fetchMock);
    render(<SkillTrainWizard {...baseProps} skills={[trainedSkill]} onAlert={onAlert} onSkillsRefresh={onSkillsRefresh} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText('Review'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-rollback-reason')).toBeInTheDocument();
    });
    // fireEvent: userEvent typing is unreliable under the fake-timer cadence.
    fireEvent.change(screen.getByTestId('wizard-rollback-reason'), { target: { value: 'operator rollback: wrong direction' } });
    await userEvent.click(screen.getByTestId('wizard-rollback-button'));
    await waitFor(() => {
      expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('Rolled back'));
    });
    // The rollback response body IS the refreshed derived state: the rail and
    // Review badges re-render stale without any registry reload.
    const badges = screen.getAllByTestId('trained-badge');
    expect(badges.length).toBeGreaterThan(0);
    for (const badge of badges) {
      expect(badge).toHaveAttribute('data-trained-state', 'stale');
    }
    expect(onSkillsRefresh).not.toHaveBeenCalled();
    const posted = fetchMock.mock.calls.find(
      ([url, init]) => String(url).endsWith('/rollback') && (init as RequestInit | undefined)?.method === 'POST',
    );
    expect(posted).toBeTruthy();
    expect(JSON.parse(String((posted![1] as RequestInit).body))).toEqual({ reason: 'operator rollback: wrong direction' });
  });

  it("renders a terminal run's Result report and opens the article by slug", async () => {
    const onNavigate = vi.fn();
    // A consistent terminal run: discovery, the job record, and the loop all end.
    vi.stubGlobal('fetch', setupFetch({ jobs: [doneJob], job: doneJob, loop: doneLoopBody }));
    render(<SkillTrainWizard {...baseProps} onNavigate={onNavigate} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText('Report'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-report-link')).toBeInTheDocument();
    });
    expect(screen.getByText('Trained Skill Result: Stability Protocol (run job-1)')).toBeInTheDocument();
    expect(screen.getByText('Completed')).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('wizard-report-link'));
    expect(onNavigate).toHaveBeenCalledWith('trained-skill-result-stability-protocol-run-job-1');
  });
});

describe('SkillTrainWizard — the wizard owns the flow (story 13)', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true, advanceTimeDelta: 25 });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const baseProps = {
    slug: 'stability-protocol',
    skills: [skill],
    onSkillsRefresh: vi.fn().mockResolvedValue(undefined),
    onNavigate: vi.fn(),
    onAlert: vi.fn(),
  };

  const queuedJob: EvolutionJob = {
    ...job,
    status: 'queued',
    iteration: 0,
    // The base fixture above carries a score'd eval; a fresh run has none.
    eval_hash: undefined,
    eval_train_count: undefined,
    eval_val_count: undefined,
    baseline_s0: undefined,
    baseline_scorer: undefined,
    eval_uploaded_at: undefined,
  };

  /** The job record as the server would move it through the journey: queued →
   * accepted (the eval gate stamps the record) → dispatched (claimed/running). */
  function journeyJobNow(state: { dispatched: boolean; dataAccepted: boolean }): EvolutionJob {
    if (state.dispatched) return job;
    if (state.dataAccepted) {
      return {
        ...queuedJob,
        eval_hash: evalMeta.eval_hash,
        eval_train_count: evalMeta.train_count,
        eval_val_count: evalMeta.val_count,
        baseline_s0: evalMeta.baseline_s0,
        baseline_scorer: evalMeta.scorer_version,
        eval_uploaded_at: evalMeta.uploaded_at,
      };
    }
    return queuedJob;
  }

  /**
   * A stateful mock of the operator-run seam: the test mutates `state` the way
   * the real server's records move, and every poll reflects it.
   */
  function setupJourneyFetch(initial: { withRun: boolean }) {
    const state = {
      started: initial.withRun,
      badData: false,
      dataAccepted: false,
      dispatched: false,
      token: 'c'.repeat(64),
    };
    const mock = vi.fn((url: string, init?: RequestInit) => {
      const method = (init?.method ?? 'GET').toUpperCase();
      if (url.includes('/api/evolution/jobs?skill=')) {
        return Promise.resolve(jsonResponse(state.started ? [journeyJobNow(state)] : []));
      }
      if (url.endsWith('/train/start') && method === 'POST') {
        state.started = true;
        return Promise.resolve({ ok: true, status: 201, json: async () => ({ job: queuedJob, token: state.token }) } as Response);
      }
      if (url.endsWith('/eval') && method === 'POST') {
        if (state.badData) {
          return Promise.resolve({
            ok: false,
            status: 400,
            json: async () => ({
              error: 'evolution eval set refused (2 issues, 2 parsed cases, user splits 0 train / 0 val)',
              issues: ['row 1: missing required field "expected" (each case needs {input, expected [, scorer]})', 'duplicate cases: rows 1, 2 are identical (input "one") — dedupe the file and re-upload'],
              parsed: 2,
              train_count: 0,
              val_count: 0,
              split_mode: 'user',
            }),
          } as Response);
        }
        state.dataAccepted = true;
        return Promise.resolve({ ok: true, status: 200, json: async () => evalMeta } as Response);
      }
      if (url.endsWith('/dispatch') && method === 'POST') {
        state.dispatched = true;
        return Promise.resolve({ ok: true, status: 200, json: async () => loopBody } as Response);
      }
      // Follow the moving records.
      const jobNow = journeyJobNow(state);
      if (url.endsWith(`/api/evolution/jobs/${jobNow.id}/eval`)) {
        return Promise.resolve(jsonResponse(evalMeta));
      }
      if (url.endsWith('/pause') || url.endsWith('/abort') || url.endsWith('/approve')) {
        return Promise.resolve(jsonResponse(jobNow));
      }
      if (url.includes('/api/evolution/candidates/')) {
        return Promise.resolve(jsonResponse(candidate));
      }
      if (url.endsWith('/api/skills/stability-protocol/audit')) {
        return Promise.resolve(jsonResponse(auditBody));
      }
      if (url.endsWith(`/api/evolution/jobs/${jobNow.id}/report`)) {
        return Promise.resolve(jsonResponse(reportView));
      }
      if (url.endsWith(`/api/evolution/jobs/${jobNow.id}/loop`)) {
        const loopBodyNow = state.dispatched ? loopBody : { loop: null, iterations: [], plateau_count: 0, max_iterations: 12, plateau_limit: 3 };
        return Promise.resolve(jsonResponse(loopBodyNow));
      }
      if (url.endsWith(`/api/evolution/jobs/${jobNow.id}`)) {
        return Promise.resolve(jsonResponse(jobNow));
      }
      return Promise.resolve(jsonResponse({}, false));
    });
    return { mock, state };
  }

  it('walks the whole journey with an action and an explanation at every step', async () => {
    const { mock, state } = setupJourneyFetch({ withRun: false });
    vi.stubGlobal('fetch', mock);

    render(<SkillTrainWizard {...baseProps} />);

    // Step 1: arriving via Train marks the skill selected, and Next is always
    // enabled with the one-line narration.
    await waitFor(() => {
      expect(screen.getByTestId('wizard-next-button')).toBeEnabled();
    });
    expect(screen.getByTestId('wizard-next-narration')).toHaveTextContent('Training Stability Protocol');

    // Step 2: guided data entry, no run yet — the primary button starts it.
    await userEvent.click(screen.getByTestId('wizard-next-button'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-start-run-button')).toBeInTheDocument();
    });
    await userEvent.click(screen.getByTestId('wizard-start-run-button'));
    await waitFor(() => {
      const posted = mock.mock.calls.find(([u, i]) => String(u).endsWith('/train/start') && (i as RequestInit | undefined)?.method === 'POST');
      expect(posted).toBeTruthy();
    });
    // The created run ID is visible immediately.
    await waitFor(() => {
      expect(screen.getByTestId('wizard-next-narration')).toHaveTextContent('Run job-1 created');
    });
    // The once-only token is held in memory, never shown uninvited.
    expect(mock.mock.calls.every(([u]) => !String(u).includes('c'.repeat(64)))).toBe(true);

    // Bad data: the gate's per-issue errors render as a fix-it list.
    state.badData = true;
    const textarea = screen.getByTestId('wizard-data-textarea');
    fireEvent.change(textarea, { target: { value: '{"input":"one"}\n{"input":"one","expected":"alpha"}' } });
    await userEvent.click(screen.getByTestId('wizard-submit-data'));
    await waitFor(() => {
      expect(screen.getAllByTestId('wizard-data-issue').length).toBe(2);
    });
    expect(screen.getByTestId('wizard-data-issues')).toHaveTextContent('nothing was stored');
    // Next stays locked until the work is done — and says why.
    expect(screen.getByTestId('wizard-next-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-next-disabled-reason')).toHaveTextContent('Get training data accepted first');

    // Valid data: accepted, dry-run + S0 preview visible, Next unlocked.
    state.badData = false;
    fireEvent.change(textarea, { target: { value: '{"input":"one","expected":"alpha"}\n{"input":"two","expected":"beta"}' } });
    await userEvent.click(screen.getByTestId('wizard-submit-data'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-data-accepted')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-data-s0-preview')).toHaveTextContent(/S0/);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-next-button')).toBeEnabled();
    });

    // Step 3: the baseline computed at upload is displayed with the note.
    await userEvent.click(screen.getByTestId('wizard-next-button'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-baseline-note')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-baseline-bar')).toBeInTheDocument();

    // Step 4: start the loop — user-initiated, with the plain-language line.
    await userEvent.click(screen.getByTestId('wizard-next-button'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-evolve-start')).toBeInTheDocument();
    });
    expect(screen.getByText(/pause or cancel it at any moment/i)).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('wizard-start-loop-button'));
    await waitFor(() => {
      const posted = mock.mock.calls.find(([u, i]) => String(u).endsWith('/dispatch') && (i as RequestInit | undefined)?.method === 'POST');
      expect(posted).toBeTruthy();
    });
    // The existing live view takes over with the audited controls visible.
    await waitFor(() => {
      expect(screen.getByTestId('wizard-pause-button')).toBeInTheDocument();
      expect(screen.getByTestId('wizard-abort-button')).toBeInTheDocument();
      expect(screen.getByTestId('wizard-approve-button')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    // No step left the user without an action: the Next bar is present here too.
    expect(screen.getByTestId('wizard-next-bar')).toBeInTheDocument();
  });

  it('auto-jumps only for runs the wizard did not start, and offers Resume for parked ones', async () => {
    const { mock, state } = setupJourneyFetch({ withRun: true });
    state.dispatched = true;
    vi.stubGlobal('fetch', mock);
    render(<SkillTrainWizard {...baseProps} />);
    // An existing live run drags the operator to Evolve (story 08 behavior).
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });

    // Park it: the paused banner points at the wizard's own Resume control.
    state.dispatched = true;
    vi.stubGlobal('fetch', mock);
    const pausedBody: EvolutionJob = { ...job, status: 'paused', checkpoint: 'paused at iteration 1' };
    mock.mockImplementation((url: string) => {
      if (String(url).endsWith('/pause')) {
        return Promise.resolve(jsonResponse(pausedBody));
      }
      if (String(url).includes('/api/evolution/jobs?skill=')) return Promise.resolve(jsonResponse([pausedBody]));
      if (String(url).endsWith('/api/evolution/jobs/job-1')) return Promise.resolve(jsonResponse(pausedBody));
      if (String(url).endsWith('/api/evolution/jobs/job-1/eval')) return Promise.resolve(jsonResponse(evalMeta));
      if (String(url).includes('/api/evolution/candidates/')) return Promise.resolve(jsonResponse(candidate));
      return Promise.resolve(jsonResponse({ loop: { job_id: 'job-1', skill_slug: 'stability-protocol', status: 'paused', current_iteration: 1, current_phase: 'maintaining', phase_status: 'interrupted', started_at: '2026-09-13T10:00:00Z', updated_at: '2026-09-13T10:01:00Z' }, iterations: [], plateau_count: 0, max_iterations: 12, plateau_limit: 3 }));
    });
    await userEvent.click(screen.getByTestId('wizard-pause-button'));
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-waiting')).toHaveTextContent(/Resume control above this header/);
    });
  });

  it('navigates "Back to skill" by bare slug so navigateTo rebuilds the viewer URL', async () => {
    const { mock } = setupJourneyFetch({ withRun: true });
    vi.stubGlobal('fetch', mock);
    const onNavigate = vi.fn();
    render(<SkillTrainWizard {...baseProps} onNavigate={onNavigate} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-run-header')).toBeInTheDocument();
    });
    // The viewer the Train button left lives at /articles/stability-protocol.
    // onNavigate is the app router's navigateTo, which prepends /articles/ to a
    // bare slug — so the wizard must pass the SLUG. A full path would
    // double-prefix into /articles//articles/<slug>, which parses as an
    // article route for a slug no article has, and App shows 404.
    await userEvent.click(screen.getByText('Back to skill'));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(onNavigate).toHaveBeenCalledWith('stability-protocol');
  });
});
