import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  WizardStepSelect,
  WizardStepData,
  WizardStepBaseline,
  WizardStepReview,
  WizardStepReport,
  WizardStepEvolveStart,
  WizardNextBar,
} from './WizardStepPanels';
import type {
  EvalMeta,
  EvolutionJob,
  IterationRecord,
  SkillAuditTrailView,
  SkillRegistryEntry,
  SkillResultReportView,
  SkillTrainedState,
  WizardEvalIssuesView,
} from '../types';

const skill: SkillRegistryEntry = {
  name: 'stability-protocol',
  title: 'Stability Protocol',
  description: 'The standing procedure.',
  version: 2,
  trained_state: { state: 'untrained', current_version: 2 },
};

const trainedSkill: SkillRegistryEntry = {
  ...skill,
  name: 'docker-cleanup',
  title: 'Docker Cleanup',
  trained_state: { state: 'trained', trained_at: '2026-09-12T14:03:00Z', trained_version: 3, trained_val_score: 0.9, current_version: 3 },
};

const job: EvolutionJob = {
  id: 'job-1',
  skill_slug: 'stability-protocol',
  profile: 'opencode',
  status: 'running',
  iteration: 1,
  max_iterations: 12,
  baseline_s0: 0.4,
  run_deadline_at: '2026-09-13T12:00:00Z',
  created_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const evalMeta: EvalMeta = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  filename: 'rounds.jsonl',
  split_mode: 'auto',
  train_count: 30,
  val_count: 10,
  eval_hash: 'a'.repeat(64),
  baseline_s0: 0.4,
  scorer_version: 'v0',
  dry_run: [
    { index: 0, input_preview: 'round 1 input', expected_preview: 'expected 1', pass: true },
    { index: 1, input_preview: 'round 2 input', expected_preview: 'expected 2', pass: false },
  ],
  estimate: { estimated_val_cases: 10, estimated_cost_usd: 0.02, budget_enforced: false, note: 'static stub' },
  uploaded_at: '2026-09-13T10:02:00Z',
};

describe('WizardStepSelect', () => {
  it('lists every skill with its trained badge and marks the selection', () => {
    render(
      <WizardStepSelect
        skills={[skill, trainedSkill]}
        selectedSlug="stability-protocol"
        loading={false}
        onSelectSkill={vi.fn()}
      />,
    );
    expect(screen.getByTestId('wizard-picker-stability-protocol')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('wizard-picker-docker-cleanup')).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByText('Untrained')).toBeInTheDocument();
    expect(screen.getByText('Trained')).toBeInTheDocument();
  });

  it('calls onSelectSkill on a picker row click', async () => {
    const onSelect = vi.fn();
    render(
      <WizardStepSelect skills={[skill]} selectedSlug="" loading={false} onSelectSkill={onSelect} />,
    );
    await userEvent.click(screen.getByTestId('wizard-picker-stability-protocol'));
    expect(onSelect).toHaveBeenCalledWith('stability-protocol');
  });

  it('renders the empty-state guidance when no skills exist', () => {
    render(<WizardStepSelect skills={[]} selectedSlug="" loading={false} onSelectSkill={vi.fn()} />);
    expect(screen.getByText('No skills registered yet')).toBeInTheDocument();
  });
});

describe('WizardStepData', () => {
  const startProps = { startBusy: false, startError: null, onStartRun: vi.fn(), uploadBusy: false, uploadIssues: null, uploadError: null, onUploadEval: vi.fn() };

  it('renders the no-run guidance honestly, with the start-run button', () => {
    render(<WizardStepData job={null} evalMeta={null} {...startProps} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-start-run-button')).toBeEnabled();
  });

  it('fires onStartRun from the start-run button', async () => {
    const onStartRun = vi.fn();
    render(<WizardStepData job={null} evalMeta={null} {...startProps} onStartRun={onStartRun} />);
    await userEvent.click(screen.getByTestId('wizard-start-run-button'));
    expect(onStartRun).toHaveBeenCalled();
  });

  it('shows the start-run error with its fix, and the busy spinner with text', () => {
    render(<WizardStepData job={null} evalMeta={null} {...startProps} startBusy startError="cannot create evolution job: skill 'x' already has an active job 'job-1' (running) — one run per skill" />);
    expect(screen.getByTestId('wizard-start-error')).toHaveTextContent('already has an active job');
    expect(screen.getByTestId('wizard-start-run-button')).toHaveTextContent('Creating the run…');
  });

  it('renders the guided data-entry form before the eval upload lands', () => {
    render(<WizardStepData job={job} evalMeta={null} {...startProps} />);
    expect(screen.getByTestId('wizard-data-form')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-data-textarea')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-submit-data')).toBeDisabled();
  });

  it('submits pasted data through the gate and renders the fix-it list verbatim on refusal', async () => {
    const onUploadEval = vi.fn();
    const issues: WizardEvalIssuesView = {
      error: 'refused',
      issues: ['train row 1: field "expected" must not be empty', 'duplicate cases: rows 1, 2 are identical'],
      parsed: 3,
      train_count: 0,
      val_count: 0,
      split_mode: 'user',
    };
    const { rerender } = render(<WizardStepData job={job} evalMeta={null} {...startProps} onUploadEval={onUploadEval} />);
    // fireEvent for typing: userEvent's keyboard parser reads { as a key key.
    const textarea = screen.getByTestId('wizard-data-textarea');
    fireEvent.change(textarea, { target: { value: '{"input":"one","expected":"alpha"}' } });
    await userEvent.click(screen.getByTestId('wizard-submit-data'));
    expect(onUploadEval).toHaveBeenCalledWith('cases.jsonl', expect.stringContaining('"input":"one"'));

    rerender(<WizardStepData job={job} evalMeta={null} {...startProps} onUploadEval={onUploadEval} uploadIssues={issues} />);
    expect(screen.getByTestId('wizard-data-issues')).toBeInTheDocument();
    expect(screen.getAllByTestId('wizard-data-issue')[0]).toHaveTextContent('train row 1: field "expected" must not be empty');
    expect(screen.getAllByTestId('wizard-data-issue')[1]).toHaveTextContent('duplicate cases: rows 1, 2 are identical');
  });

  it('renders the accepted upload summary from server state', () => {
    render(<WizardStepData job={job} evalMeta={evalMeta} {...startProps} />);
    expect(screen.getByTestId('wizard-data-accepted')).toBeInTheDocument();
    expect(screen.getByText('Train cases')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.getByText('rounds.jsonl')).toBeInTheDocument();
    expect(screen.getAllByText(/eval hash/).length).toBeGreaterThan(0);
    expect(screen.getByTestId('wizard-data-s0-preview')).toHaveTextContent(/S0/);
    expect(screen.getByTestId('wizard-data-replace')).toBeInTheDocument();
  });
});

describe('WizardStepBaseline', () => {
  it('renders the no-run and no-baseline waiting states', () => {
    render(<WizardStepBaseline job={null} evalMeta={null} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
    // A job without a scored baseline is still waiting; the fixture job carries
    // S0 only once the eval gate ran.
    const { rerender } = render(<WizardStepBaseline job={{ ...job, baseline_s0: undefined }} evalMeta={null} />);
    expect(screen.getByText('No baseline yet')).toBeInTheDocument();
    rerender(<WizardStepBaseline job={job} evalMeta={evalMeta} />);
    expect(screen.getByText('40%')).toBeInTheDocument();
  });

  it('renders S0, the dry-run samples, and the estimate stub', () => {
    render(<WizardStepBaseline job={job} evalMeta={evalMeta} />);
    expect(screen.getByText('40%')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-baseline-bar')).toBeInTheDocument();
    expect(screen.getByText('Dry run (2 val samples)')).toBeInTheDocument();
    expect(screen.getByText(/\[0\] round 1 input/)).toBeInTheDocument();
    expect(screen.getByText(/Cost estimate/)).toBeInTheDocument();
  });
});

describe('WizardStepReview', () => {
  const iterations: IterationRecord[] = [
    {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      iteration: 1,
      started_at: '2026-09-13T10:00:00Z',
      completed_at: '2026-09-13T10:05:00Z',
      phases: [],
      candidate_id: 'cand-1',
      pattern_slugs: ['pattern-a', 'pattern-b'],
      val_score: 0.55,
      r_best: 0.4,
      outcome: 'accepted',
      result_version: 4,
    },
    {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      iteration: 2,
      started_at: '2026-09-13T10:06:00Z',
      phases: [],
      candidate_id: 'cand-2',
      pattern_slugs: ['pattern-b'],
      val_score: 0.5,
      r_best: 0.55,
      outcome: 'rejected',
    },
  ];

  const auditBody: SkillAuditTrailView = {
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
      {
        candidate_id: 'trained-marker-revoke',
        skill_slug: 'stability-protocol',
        parent_version: 4,
        content_hash: 'h2',
        validation_score: 0.55,
        r_best_before: 0.55,
        r_best_after: 0.55,
        outcome: 'revoked',
        decider: 'human',
        scorer_version: 'v0',
        reason: 'operator rollback: wrong direction',
        timestamp: '2026-09-13T10:30:00Z',
      },
    ],
  };

  const trainedOk: SkillTrainedState = {
    state: 'trained',
    trained_at: '2026-09-13T10:05:00Z',
    trained_version: 4,
    trained_val_score: 0.55,
    current_version: 4,
  };
  const trainedStale: SkillTrainedState = {
    ...trainedOk,
    state: 'stale',
    reason: 'eval set changed after training (trained on aaaa, current bbbb)',
  };

  const rolledBackEntry = {
    slug: 'stability-protocol',
    title: 'Stability Protocol',
    state: 'stale' as const,
    revoked_at: '2026-09-13T11:00:00Z',
    revoked_reason: 'operator rollback: wrong direction',
    trained_version: 4,
    trained_parent_version: 3,
    trained_val_score: 0.55,
    current_version: 3,
  };

  function jsonResponse(body: unknown, ok = true, status = ok ? 200 : 500) {
    return { ok, status, json: async () => body } as Response;
  }

  /** Mocks the audit endpoint (and anything else the panes fetch). */
  function setupFetch(overrides: Partial<Record<string, unknown>> = {}) {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars -- init kept so mock calls record POST metadata for assertions
    return vi.fn((url: string, _init?: RequestInit) => {
      if (url.endsWith('/api/skills/stability-protocol/audit')) {
        if (overrides['auditStatus'] !== undefined) {
          return Promise.resolve(jsonResponse({ error: overrides['auditError'] ?? 'boom' }, false, overrides['auditStatus'] as number));
        }
        return Promise.resolve(jsonResponse(overrides['audit'] ?? auditBody));
      }
      if (url.endsWith('/api/skills/stability-protocol/rollback')) {
        if (overrides['rollbackStatus'] !== undefined) {
          return Promise.resolve(jsonResponse({ error: overrides['rollbackError'] ?? 'cannot roll back' }, false, overrides['rollbackStatus'] as number));
        }
        return Promise.resolve(jsonResponse(overrides['rollback'] ?? rolledBackEntry));
      }
      return Promise.resolve(jsonResponse({}, false, 404));
    });
  }

  beforeEach(() => {
    vi.stubGlobal('confirm', vi.fn(() => true));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const reviewProps = (overrides: Partial<Parameters<typeof WizardStepReview>[0]> = {}) => ({
    slug: 'stability-protocol',
    job,
    evalMeta,
    iterations,
    trainedState: trainedOk as SkillTrainedState | null,
    onBack: vi.fn(),
    onRetrain: vi.fn(),
    onTrainedStateUpdate: vi.fn(),
    onAlert: vi.fn(),
    ...overrides,
  });

  it('renders the honest no-run guidance without a job', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps({ job: null })} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
    expect(screen.queryByTestId('wizard-review-baseline-final')).not.toBeInTheDocument();
  });

  it('renders the baseline-vs-final table from S0 and the audited R_best', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-r-best')).toHaveTextContent('55% (R_best)');
    });
    expect(screen.getByText('40% (S0, v0 scorer)')).toBeInTheDocument();
    expect(screen.getByText(/Δ vs baseline: \+0\.1500 \(S0 0\.4000 → R_best 0\.5500\)/)).toBeInTheDocument();
  });

  it('renders the iteration history with candidate, val score vs R_best, and outcome', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-iterations')).toBeInTheDocument();
    });
    // cand-1 also appears in the audit table below; scope to the iteration table.
    expect(within(screen.getByTestId('wizard-review-iterations')).getByText('cand-1')).toBeInTheDocument();
    expect(within(screen.getByTestId('wizard-review-iterations')).getByText('0.5500 vs 0.4000')).toBeInTheDocument();
    expect(within(screen.getByTestId('wizard-review-iterations')).getByText('Accepted (promoted v4)')).toBeInTheDocument();
    expect(within(screen.getByTestId('wizard-review-iterations')).getByText('Rejected')).toBeInTheDocument();
  });

  it('renders eval hash + counts as the trained marker lineage', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-audit')).toBeInTheDocument();
    });
    expect(screen.getByText('Train cases')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText(/eval hash aaaaaaaaaaaaaaaaaaaaaaaa…/)).toBeInTheDocument();
  });

  it('renders the staleness banner from the derived trained state', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps({ trainedState: trainedStale })} />);
    const badge = screen.getAllByTestId('trained-badge')[0];
    expect(badge).toHaveAttribute('data-trained-state', 'stale');
    expect(screen.getByText(/eval set changed after training/)).toBeInTheDocument();
  });

  it('summarizes the limitations honestly: patterns count, no pruning, scorer v0', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-audit')).toBeInTheDocument();
    });
    expect(screen.getByText(/Patterns added: 2\./)).toBeInTheDocument();
    expect(screen.getByText(/it never prunes, deduplicates, or rewrites existing wiki sections/)).toBeInTheDocument();
    expect(screen.getByText(/Scorer v0: the gate compares candidate structure, not model answers/)).toBeInTheDocument();
  });

  it('renders the skill-wide audit trail with accept and revoke decisions', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps()} />);
    // The audit table loads async; wait for audit-only content before asserting.
    await waitFor(() => {
      expect(within(screen.getByTestId('wizard-review-audit')).getByText('Revoked')).toBeInTheDocument();
    });
    expect(screen.getAllByText('Accepted').length).toBeGreaterThan(0);
    expect(screen.getByText('0.4000 → 0.5500')).toBeInTheDocument();
    const revokeRow = within(screen.getByTestId('wizard-review-audit')).getByText('Revoked').closest('tr');
    expect(revokeRow).toBeTruthy();
    expect(revokeRow!.getAttribute('title')).toBe('operator rollback: wrong direction');
  });

  it('keeps the final cell honest when the audit trail is unreachable', async () => {
    vi.stubGlobal('fetch', setupFetch({ auditStatus: 404, auditError: "'stability-protocol' is not a Custom AI Skill" }));
    render(<WizardStepReview {...reviewProps()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-review-audit-error')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-review-r-best')).toHaveTextContent('— (audit trail unreachable)');
  });

  it('rolls back through the REST endpoint and re-renders the derived state from the response', async () => {
    const fetchMock = setupFetch();
    vi.stubGlobal('fetch', fetchMock);
    const onTrainedStateUpdate = vi.fn();
    const onAlert = vi.fn();
    render(<WizardStepReview {...reviewProps({ onTrainedStateUpdate, onAlert })} />);

    await userEvent.type(screen.getByTestId('wizard-rollback-reason'), 'operator rollback: wrong direction');
    await userEvent.click(screen.getByTestId('wizard-rollback-button'));

    await waitFor(() => {
      expect(onTrainedStateUpdate).toHaveBeenCalledWith(rolledBackEntry);
    });
    expect(onAlert).toHaveBeenCalledWith(
      'success',
      'Rolled back — the skill body is restored to its pre-training version and the trained marker is revoked with your reason.',
    );
    const posted = fetchMock.mock.calls.find(
      ([url, init]) => String(url).endsWith('/rollback') && (init as RequestInit).method === 'POST',
    );
    expect(posted).toBeTruthy();
    expect(JSON.parse(String((posted![1] as RequestInit).body))).toEqual({ reason: 'operator rollback: wrong direction' });
    // The revocation is a real audit record now: the trail was reloaded.
    const auditCalls = fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/audit'));
    expect(auditCalls.length).toBe(2);
    expect(vi.mocked(confirm)).toHaveBeenCalled();
  });

  it('surfaces rollback failures as error toasts', async () => {
    vi.stubGlobal('fetch', setupFetch({ rollbackStatus: 409, rollbackError: "cannot roll back skill 'x' while evolution job 'y' is running — cancel or finish it first" }));
    const onAlert = vi.fn();
    render(<WizardStepReview {...reviewProps({ onAlert })} />);
    await userEvent.type(screen.getByTestId('wizard-rollback-reason'), 'too soon');
    await userEvent.click(screen.getByTestId('wizard-rollback-button'));
    await waitFor(() => {
      expect(onAlert).toHaveBeenCalledWith(
        'error',
        "cannot roll back skill 'x' while evolution job 'y' is running — cancel or finish it first",
      );
    });
  });

  it('disables the rollback control without a trained marker', async () => {
    vi.stubGlobal('fetch', setupFetch());
    render(<WizardStepReview {...reviewProps({ trainedState: { state: 'untrained', current_version: 2 } })} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-rollback-button')).toBeInTheDocument();
    });
    expect(screen.getByTestId('wizard-rollback-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-rollback-button')).toHaveAttribute('title', 'No trained marker to roll back');
  });

  it('re-trains by restarting the flow at the picker', async () => {
    vi.stubGlobal('fetch', setupFetch());
    const onRetrain = vi.fn();
    render(<WizardStepReview {...reviewProps({ onRetrain })} />);
    await userEvent.click(screen.getByTestId('wizard-retrain-button'));
    expect(onRetrain).toHaveBeenCalled();
  });
});

describe('WizardStepReport', () => {
  const reportRef = {
    slug: 'trained-skill-result-stability-protocol-run-job-1',
    title: 'Trained Skill Result: Stability Protocol (run job-1)',
    url: '/articles/trained-skill-result-stability-protocol-run-job-1',
    created_at: '2026-09-13T10:20:00Z',
  };

  function jsonResponse(body: unknown, ok = true, status = ok ? 200 : 500) {
    return { ok, status, json: async () => body } as Response;
  }

  function setupFetch(body: unknown, ok = true, status = ok ? 200 : 500) {
    return vi.fn((url: string) => {
      if (url.endsWith('/api/evolution/jobs/job-1/report')) {
        return Promise.resolve(jsonResponse(body, ok, status));
      }
      return Promise.resolve(jsonResponse({}, false, 404));
    });
  }

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders the honest no-run guidance without a job', () => {
    render(<WizardStepReport jobId={null} onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
  });

  it('shows that the report publishes when the run ends while it is still live', async () => {
    const live: SkillResultReportView = {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      status: 'running',
      terminal: false,
    };
    vi.stubGlobal('fetch', setupFetch(live));
    render(<WizardStepReport jobId="job-1" onBack={vi.fn()} onNavigate={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-report-pending')).toBeInTheDocument();
    });
    expect(screen.getByText(/The report publishes when the run ends/)).toBeInTheDocument();
    expect(screen.queryByTestId('wizard-report-link')).not.toBeInTheDocument();
  });

  it('renders the report metadata and the article link once the run ended', async () => {
    const done: SkillResultReportView = {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      status: 'complete',
      loop_outcome: 'completed',
      terminal: true,
      report: reportRef,
      trained_state: { state: 'trained', trained_version: 4, trained_val_score: 0.55, current_version: 4 },
    };
    const onNavigate = vi.fn();
    vi.stubGlobal('fetch', setupFetch(done));
    render(<WizardStepReport jobId="job-1" onBack={vi.fn()} onNavigate={onNavigate} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-report-link')).toBeInTheDocument();
    });
    expect(screen.getByText('Trained Skill Result: Stability Protocol (run job-1)')).toBeInTheDocument();
    expect(screen.getByText('Completed')).toBeInTheDocument();
    expect(screen.getByText(reportRef.url)).toBeInTheDocument();
    expect(screen.getByTestId('wizard-report-trained-state')).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('wizard-report-link'));
    expect(onNavigate).toHaveBeenCalledWith('trained-skill-result-stability-protocol-run-job-1');
  });

  it('reads a 404 as the honest no-report-yet state', async () => {
    vi.stubGlobal('fetch', setupFetch({ error: "evolution job 'job-1' not found" }, false, 404));
    render(<WizardStepReport jobId="job-1" onBack={vi.fn()} onNavigate={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByText('No report exists for this run yet')).toBeInTheDocument();
    });
    expect(screen.getByText(/The report publishes when the run ends/)).toBeInTheDocument();
  });

  it('says so honestly when the run ended but no report article exists', async () => {
    const doneNoReport: SkillResultReportView = {
      job_id: 'job-1',
      skill_slug: 'stability-protocol',
      status: 'cancelled',
      loop_outcome: 'cancelled',
      terminal: true,
    };
    vi.stubGlobal('fetch', setupFetch(doneNoReport));
    render(<WizardStepReport jobId="job-1" onBack={vi.fn()} onNavigate={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByTestId('wizard-report-missing')).toBeInTheDocument();
    });
    expect(screen.getByText(/no report article was found for it/)).toBeInTheDocument();
  });

  it('surfaces non-404 failures as a load error', async () => {
    vi.stubGlobal('fetch', setupFetch({ error: 'storage locked' }, false, 500));
    render(<WizardStepReport jobId="job-1" onBack={vi.fn()} onNavigate={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByText('The report could not be loaded')).toBeInTheDocument();
    });
    expect(screen.getByText(/storage locked/)).toBeInTheDocument();
  });
});

describe('WizardStepEvolveStart', () => {
  const queued: EvolutionJob = { ...job, status: 'queued', iteration: 0 };
  const paused: EvolutionJob = { ...job, status: 'paused', checkpoint: 'paused at iteration 1' };

  it('offers the start-the-loop button with the plain-language contract', () => {
    render(<WizardStepEvolveStart job={queued} dispatchBusy={false} dispatchError={null} onStartLoop={vi.fn()} />);
    expect(screen.getByTestId('wizard-evolve-start')).toBeInTheDocument();
    expect(screen.getByText(/pause or cancel it at any moment/i)).toBeInTheDocument();
    expect(screen.getByText(/90-minute cap/i)).toBeInTheDocument();
    expect(screen.getByTestId('wizard-start-loop-button')).toHaveTextContent('Start the loop');
  });

  it('fires onStartLoop with the task line', async () => {
    const onStartLoop = vi.fn();
    render(<WizardStepEvolveStart job={queued} dispatchBusy={false} dispatchError={null} onStartLoop={onStartLoop} />);
    await userEvent.click(screen.getByTestId('wizard-start-loop-button'));
    expect(onStartLoop).toHaveBeenCalledWith('');
  });

  it('renders the resume shape for a parked run', () => {
    render(<WizardStepEvolveStart job={paused} dispatchBusy={false} dispatchError={null} onStartLoop={vi.fn()} resuming />);
    expect(screen.getByRole('heading', { name: 'Resume the run' })).toBeInTheDocument();
    expect(screen.getByTestId('wizard-start-loop-button')).toHaveTextContent('Resume the run');
    expect(screen.getByText(/re-queues it from the checkpoint/i)).toBeInTheDocument();
  });

  it('shows the busy text and the dispatch error with its fix', () => {
    render(<WizardStepEvolveStart job={queued} dispatchBusy dispatchError="cannot dispatch: at capacity" onStartLoop={vi.fn()} />);
    expect(screen.getByTestId('wizard-start-loop-button')).toHaveTextContent('Starting the loop…');
    expect(screen.getByTestId('wizard-dispatch-error')).toHaveTextContent('cannot dispatch: at capacity');
  });
});

describe('WizardNextBar', () => {
  it('renders narration, the enabled Next, and its hint', async () => {
    const onNext = vi.fn();
    render(<WizardNextBar narration="Run job-1 created — the skill is locked." nextLabel="Next: Data" nextHint="Paste your cases." onNext={onNext} enabled />);
    expect(screen.getByTestId('wizard-next-narration')).toHaveTextContent('Run job-1 created — the skill is locked.');
    expect(screen.getByTestId('wizard-next-button')).toBeEnabled();
    await userEvent.click(screen.getByTestId('wizard-next-button'));
    expect(onNext).toHaveBeenCalled();
    expect(screen.getByText('Paste your cases.')).toBeInTheDocument();
  });

  it('disables Next with the reason always visible', () => {
    render(<WizardNextBar narration="No run yet." nextLabel="Next: Baseline" onNext={vi.fn()} enabled={false} disabledReason="Create the run first." />);
    expect(screen.getByTestId('wizard-next-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-next-disabled-reason')).toHaveTextContent('Create the run first.');
  });
});
