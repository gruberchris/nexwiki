import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { EvalMeta, EvolutionJob, SkillRegistryEntry, SkillTrainedState, SkillTrainedStateEntry, WizardEvalIssuesView, WizardTrainStartView } from '../types';
import { useEvolutionRun, isEvolutionJobTerminal } from '../hooks/useEvolutionRun';
import { useSplitPane } from '../hooks/useSplitPane';
import { pickFollowedJob } from './wizardChips';
import { ChevronLeft, ChevronRight, ArrowLeft, Wrench, Copy, Check } from 'lucide-react';
import { WizardStepper, type WizardStepId, type WizardStepInfo } from './WizardStepper';
import { WizardRunHeader, type WizardControl } from './WizardRunHeader';
import { WizardScoreStrip } from './WizardScoreStrip';
import { WizardIterationCard } from './WizardIterationCard';
import { WizardStepSelect, WizardStepData, WizardStepBaseline, WizardStepReview, WizardStepReport, WizardStepEvolveStart, WizardNextBar } from './WizardStepPanels';
import { TrainedBadge } from './TrainedBadge';

/**
 * The WikiSkill evolution wizard (story 08, Phase D/D1 + D2; story 09, Phase
 * D3; restructured by story 13 so the wizard owns the flow): a full page at
 * /skills/<slug>/train with the six-step rail on the left and the detail pane
 * on the right, reusing the app's minimizable-sidebar and split-resize
 * patterns and only the theme tokens.
 *
 * STORY 13 — A WIZARD IS USER-INITIATED AT EVERY STEP. Arriving via Train
 * marks the skill selected; every step ends with one line of narration and a
 * Next action that is enabled exactly when the step's work is done. The
 * browser itself starts the run (POST /train/start), walks the training data
 * through the real story-05 gate (POST /eval), and dispatches the loop
 * (POST /dispatch) — no step leaves the operator waiting on an unseen process:
 * loading states show spinners with text, errors show the fix, and the live
 * view polls the run every 2 s.
 */

export type ToastFn = (type: 'success' | 'error', text: string) => void;

interface SkillTrainWizardProps {
  slug: string;
  skills: SkillRegistryEntry[];
  onSkillsRefresh: () => Promise<void> | void;
  /**
   * In-app navigation, wired to the app router's navigateTo: pass the article
   * SLUG (not a full path) — navigateTo builds the /articles/{slug} URL.
   * (Passing "/articles/<slug>" here would yield "/articles//articles/<slug>",
   * which the router parses as a nonexistent article and shows 404.)
   */
  onNavigate: (url: string) => void;
  onAlert: ToastFn;
}
/** Elapsed between the run's start and its end (or "now" while live); 0 when unknown. */
function computeElapsedMs(startedAt?: string, createdAt?: string, completedAt?: string, nowMs = Date.now()): number {
  const startedAtReal = startedAt && !startedAt.startsWith('0001-01-01') ? startedAt : undefined;
  const createdAtReal = createdAt && !createdAt.startsWith('0001-01-01') ? createdAt : undefined;
  const started = startedAtReal ?? createdAtReal;
  if (!started) return 0;
  const completedReal = completedAt && !completedAt.startsWith('0001-01-01') ? completedAt : undefined;
  const end = completedReal ? new Date(completedReal).getTime() : nowMs;
  return Math.max(0, end - new Date(started).getTime());
}

const ACTIVE_JOB_POLL_MS = 4000;

/** The token disclosure: the start response returned the harness token once,
 * for the operator who prefers their own CLI. In-memory only, never logged. */
const WizardTokenDisclosure: React.FC<{ token: string; jobId: string; onAlert: ToastFn }> = ({ token, jobId, onAlert }) => {
  const [copied, setCopied] = useState(false);
  const copy = useCallback(() => {
    void navigator.clipboard
      ?.writeText(token)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => onAlert('error', 'The browser blocked clipboard access — select the token text and copy it manually.'));
  }, [token, onAlert]);
  return (
    <details data-testid="wizard-token-disclosure" className="rounded-2xl border border-dashed border-themeBorder bg-themeBgSecondary/40 p-4">
      <summary className="text-[11px] font-bold text-themeAccent cursor-pointer">Run token (for your own CLI — shown once, never stored)</summary>
      <p className="mt-2 text-[10px] text-themeTextMuted leading-relaxed">
        The server keeps this token in memory for its own dispatch calls; the wizard needs nothing from
        you. If you would rather drive run {jobId} with your own CLI, copy it now — it is never retrievable
        again and never logged.
      </p>
      <div className="mt-2 flex items-center gap-2">
        <code data-testid="wizard-token-value" className="text-[10px] font-mono break-all text-themeTextMuted flex-1 min-w-0">{token}</code>
        <button
          type="button"
          onClick={copy}
          data-testid="wizard-token-copy"
          title="Copy the run token"
          aria-label="Copy the run token"
          className="p-2 rounded-xl border border-themeBorder bg-themeBgPrimary text-themeTextMuted hover:text-themeAccent hover:border-themeAccent/40 transition-colors cursor-pointer shrink-0"
        >
          {copied ? <Check size={12} /> : <Copy size={12} />}
        </button>
      </div>
    </details>
  );
};

export const SkillTrainWizard: React.FC<SkillTrainWizardProps> = ({
  slug,
  skills,
  onSkillsRefresh,
  onNavigate,
  onAlert,
}) => {
  const skill = skills.find((entry) => entry.name === slug) ?? null;

  // --- derived trained state, with the rollback path's override (story 09):
  // POST /api/skills/{slug}/rollback answers with the refreshed derived state,
  // so the badges and the Review banner re-render from that body directly —
  // no registry reload gymnastics.
  const [trainedStateOverrides, setTrainedStateOverrides] = useState<Record<string, SkillTrainedState>>({});
  const handleTrainedStateUpdate = useCallback((state: SkillTrainedStateEntry) => {
    setTrainedStateOverrides((prev) => ({ ...prev, [state.slug]: state }));
  }, []);
  const trainedState: SkillTrainedState | null = skill
    ? (trainedStateOverrides[slug] ?? skill.trained_state)
    : (trainedStateOverrides[slug] ?? null);

  // --- run discovery: follow the skill's newest job, re-checking on a slow
  // loop so a new dispatch (or a terminal run followed by a retrain) is picked
  // up without losing the followed one. A run the wizard itself just started
  // (step 2) is followed explicitly and immediately, without waiting a tick.
  const [pickedJobId, setPickedJobId] = useState<string | null>(null);
  const [explicitJobId, setExplicitJobId] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    const discover = async () => {
      try {
        const res = await fetch(`/api/evolution/jobs?skill=${encodeURIComponent(slug)}`);
        if (!res.ok) return;
        const jobs: EvolutionJob[] = await res.json();
        if (cancelled) return;
        const picked = pickFollowedJob(Array.isArray(jobs) ? jobs : []);
        setPickedJobId(picked?.id ?? null);
      } catch {
        // Transient: the next tick retries.
      }
    };
    void discover();
    const timer = setInterval(discover, ACTIVE_JOB_POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [slug]);
  const followedJobId = explicitJobId ?? pickedJobId;

  // The run the wizard started in this session (so the live-view auto-jump
  // does not yank the operator off the walk they are on), and the harness
  // token the start response returned once. Both in-memory only.
  const wizardCreatedJob = useRef<string | null>(null);
  const runTokenRef = useRef<string | null>(null);
  const [knownToken, setKnownToken] = useState<string | null>(null);

  const run = useEvolutionRun(followedJobId);
  const { job, loop, iterations, candidates } = run;

  // A run that just ended may have promoted a candidate (stamping the trained
  // marker) — refresh the registry once so the badge tells the new truth.
  const refreshedTerminal = useRef(false);
  useEffect(() => {
    if (!job) return;
    if (isEvolutionJobTerminal(job.status)) {
      if (!refreshedTerminal.current) {
        refreshedTerminal.current = true;
        void onSkillsRefresh();
      }
    } else {
      refreshedTerminal.current = false;
    }
  }, [job, onSkillsRefresh]);

  // --- step state + readiness from server truth
  const [step, setStep] = useState<WizardStepId>(1);
  // The auto-jump to the live view is keyed by the job being watched: the run
  // that was on screen when the wizard opened (or was re-trained past) drags
  // the operator to Evolve once, while a fresh dispatch — a new job id — gets
  // the jump again. A run the wizard created from the Data step never jumps:
  // the operator is walking the flow, not returning to watch.
  const autoJumpedFor = useRef<string | null>(null);
  useEffect(() => {
    if (job && autoJumpedFor.current !== job.id) {
      if (wizardCreatedJob.current === job.id) {
        autoJumpedFor.current = job.id;
        return;
      }
      autoJumpedFor.current = job.id;
      setStep(4);
    }
  }, [job]);

  // --- operator-run handlers (story 13). Every one has a busy flag, an error
  // with its fix, and a success toast — nothing async runs without visible
  // state.
  const [startBusy, setStartBusy] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [uploadBusy, setUploadBusy] = useState(false);
  const [uploadIssues, setUploadIssues] = useState<WizardEvalIssuesView | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [dispatchBusy, setDispatchBusy] = useState(false);
  const [dispatchError, setDispatchError] = useState<string | null>(null);

  const startRun = useCallback(async () => {
    if (startBusy) return;
    setStartBusy(true);
    setStartError(null);
    try {
      const res = await fetch(`/api/skills/${encodeURIComponent(slug)}/train/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}),
      });
      if (!res.ok) {
        const errBody = await res.json().catch(() => ({}));
        throw new Error(errBody.error || `The run could not be created (${res.status})`);
      }
      const view = (await res.json()) as WizardTrainStartView;
      runTokenRef.current = view.token;
      setKnownToken(view.token);
      wizardCreatedJob.current = view.job.id;
      autoJumpedFor.current = view.job.id;
      setExplicitJobId(view.job.id);
      onAlert('success', `Run ${view.job.id} created — the skill is locked until the run ends.`);
    } catch (err: unknown) {
      setStartError(err instanceof Error ? err.message : 'The run could not be created');
    } finally {
      setStartBusy(false);
    }
  }, [startBusy, slug, onAlert]);

  const uploadEval = useCallback(
    async (filename: string, content: string) => {
      if (!job || uploadBusy) return;
      setUploadBusy(true);
      setUploadIssues(null);
      setUploadError(null);
      try {
        const body: Record<string, string> = { filename, content };
        if (runTokenRef.current) body.job_token = runTokenRef.current;
        const res = await fetch(`/api/evolution/jobs/${job.id}/eval`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!res.ok) {
          const errBody = (await res.json().catch(() => ({}))) as Partial<WizardEvalIssuesView> & { error?: string };
          if (res.status === 400 && Array.isArray(errBody.issues) && errBody.issues.length > 0) {
            // The story-05 gate refused: every issue, verbatim, as a fix-it list.
            setUploadIssues(errBody as WizardEvalIssuesView);
            return;
          }
          throw new Error(errBody.error || `The training data was refused (${res.status})`);
        }
        const meta = (await res.json()) as EvalMeta;
        onAlert(
          'success',
          `Training data accepted — ${meta.train_count} train / ${meta.val_count} val stored, baseline S0 scored at ${Math.round((meta.baseline_s0 ?? 0) * 10000) / 100}%.`,
        );
        await run.refresh();
      } catch (err: unknown) {
        setUploadError(err instanceof Error ? err.message : 'The training data could not be submitted');
      } finally {
        setUploadBusy(false);
      }
    },
    [job, uploadBusy, run, onAlert],
  );

  const dispatchLoop = useCallback(
    async (task: string) => {
      if (!job || dispatchBusy) return;
      setDispatchBusy(true);
      setDispatchError(null);
      try {
        const body: Record<string, string> = {};
        if (task.trim()) body.task = task.trim();
        if (runTokenRef.current) body.job_token = runTokenRef.current;
        const res = await fetch(`/api/evolution/jobs/${job.id}/dispatch`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        if (!res.ok) {
          const errBody = await res.json().catch(() => ({}));
          throw new Error(errBody.error || `The loop could not start (${res.status})`);
        }
        onAlert('success', 'The loop is driving — iterations appear below as each step lands.');
        await run.refresh();
      } catch (err: unknown) {
        setDispatchError(err instanceof Error ? err.message : 'The loop could not start');
      } finally {
        setDispatchBusy(false);
      }
    },
    [job, dispatchBusy, run, onAlert],
  );

  const steps: WizardStepInfo[] = useMemo(() => {
    const hasJob = !!job;
    const hasEval = !!run.evalMeta;
    const s0 = run.evalMeta?.baseline_s0 ?? job?.baseline_s0 ?? 0;
    const hasBaseline = hasEval || s0 > 0;
    const isTerminal = !!job && isEvolutionJobTerminal(job.status);
    // Status comes from server truth, never from the operator's clicks: a step
    // is done when the run actually passed it, locked when nothing can show
    // there yet, and the rail highlights the step being viewed separately.
    return [
      { id: 1, title: 'Select', hint: skill ? `Training ${skill.title}` : 'Pick the skill to evolve', status: 'done' as const },
      {
        id: 2,
        title: 'Data',
        hint: hasEval
          ? `${run.evalMeta?.train_count ?? 0} train / ${run.evalMeta?.val_count ?? 0} val`
          : hasJob
            ? 'Waiting for the eval upload'
            : 'No run yet',
        status: hasEval ? ('done' as const) : hasJob ? ('available' as const) : ('locked' as const),
      },
      {
        id: 3,
        title: 'Baseline',
        hint: hasBaseline ? `S0 ${Math.round(s0 * 10000) / 10000}` : 'Scored with the eval set',
        status: hasBaseline ? ('done' as const) : hasJob ? ('available' as const) : ('locked' as const),
      },
      {
        id: 4,
        title: 'Evolve',
        hint: hasJob ? `Run ${job?.status}` : 'No run yet',
        status: hasJob ? ('available' as const) : ('locked' as const),
      },
      {
        id: 5,
        title: 'Review',
        hint: hasJob ? `${iterations.length} recorded iterations` : 'No run yet',
        status: hasJob ? ('available' as const) : ('locked' as const),
      },
      {
        id: 6,
        title: 'Report',
        hint: !hasJob ? 'No run yet' : isTerminal ? 'Result report ready' : 'Publishes when the run ends',
        status: hasJob && isTerminal ? ('available' as const) : ('locked' as const),
      },
    ];
  }, [job, run.evalMeta, iterations.length, skill]);

  // --- elapsed ticking for the run header
  const [now, setNow] = useState(() => Date.now());
  const isLive = !!job && !isEvolutionJobTerminal(job.status);
  useEffect(() => {
    if (!isLive) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [isLive]);
  // Plain call, not a hook: cheap arithmetic, and the compiler-friendly shape
  // keeps the component inside the React Compiler's memoization model.
  const elapsedMs = computeElapsedMs(loop?.started_at, job?.created_at, job?.completed_at, now);

  // --- human controls, via the audited REST endpoints
  const [controlBusy, setControlBusy] = useState<WizardControl | null>(null);

  const runControl = useCallback(
    async (control: WizardControl, url: string, body?: Record<string, string>) => {
      setControlBusy(control);
      try {
        const res = await fetch(url, {
          method: 'POST',
          headers: body ? { 'Content-Type': 'application/json' } : undefined,
          body: body ? JSON.stringify(body) : undefined,
        });
        if (!res.ok) {
          const errBody = await res.json().catch(() => ({}));
          throw new Error(errBody.error || `The ${control} request failed (${res.status})`);
        }
        const messages: Record<WizardControl, string> = {
          pause: 'Pause requested — the run parks at its next step boundary.',
          abort: 'Run aborted — the skill stays at its last accepted version.',
          approve: 'Approve-early requested — the loop finishes with the current best.',
        };
        onAlert('success', messages[control]);
        await run.refresh();
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : `The ${control} request failed`;
        onAlert('error', msg);
      } finally {
        setControlBusy(null);
      }
    },
    [onAlert, run],
  );

  const handlePause = useCallback(() => {
    if (!job) return;
    void runControl('pause', `/api/evolution/jobs/${job.id}/pause`, {
      checkpoint: `paused from the wizard at iteration ${loop?.current_iteration ?? job.iteration}`,
    });
  }, [job, loop?.current_iteration, runControl]);

  const handleAbort = useCallback(() => {
    if (!job) return;
    if (!window.confirm(`Abort run ${job.id}? The skill stays at its last accepted version and the wiki is untouched.`)) return;
    void runControl('abort', `/api/evolution/jobs/${job.id}/abort`, { reason: 'aborted from the wizard' });
  }, [job, runControl]);

  const handleApprove = useCallback(() => {
    if (!job) return;
    if (!window.confirm('Approve the current best candidate early and finish the run? The gate still decides: an accept promotes, a rejection keeps the skill at its last accepted version.')) return;
    void runControl('approve', `/api/evolution/jobs/${job.id}/approve`);
  }, [job, runControl]);

  // Re-train (story 09 Review step): restart the flow at the picker. The run
  // just reviewed is marked as watched so it cannot drag the operator back to
  // the live view, while the discovery loop keeps polling — a fresh dispatch
  // is a new job id and gets the auto-jump again.
  const handleRetrain = useCallback(() => {
    if (job) autoJumpedFor.current = job.id;
    setExplicitJobId(null);
    setStep(1);
  }, [job]);

  // --- layout: minimizable rail + split-resize detail pane (App sidebar pattern)
  const [railMinimized, setRailMinimized] = useState(false);
  const { splitPercentage, isDragging, containerRef, startResizing } = useSplitPane(26);

  const currentIteration = loop?.status !== 'terminal' ? loop?.current_iteration : undefined;

  const handleSelectSkill = useCallback(
    (nextSlug: string) => {
      if (nextSlug !== slug) onNavigate(`/skills/${nextSlug}/train`);
    },
    [slug, onNavigate],
  );

  // The per-step narration + Next readiness (story 13): what the previous
  // action did, and what Next will do.
  const nextBarFor = (current: WizardStepId): React.ReactNode => {
    if (current === 1) {
      return (
        <WizardNextBar
          narration={skill ? `Training ${skill.title} — step 2 starts the run (which locks the skill until it ends).` : 'Pick a skill first — the rows above carry each skill\'s derived trained state.'}
          nextLabel="Next: Data"
          nextHint="Paste or upload your training cases; the wizard walks the format gate with you."
          onNext={() => setStep(2)}
          enabled
        />
      );
    }
    if (current === 2) {
      if (!job) {
        return (
          <WizardNextBar
            narration="No run yet — starting one locks the skill until the run ends (one run per skill)."
            nextLabel="Next: Baseline"
            nextHint="The baseline is scored automatically when your data passes the gate."
            onNext={() => setStep(3)}
            enabled={false}
            disabledReason="Create the run and get training data accepted first — the button above does both."
          />
        );
      }
      if (run.evalMeta) {
        return (
          <WizardNextBar
            narration={`Run ${job.id} — accepted ${run.evalMeta.filename}: ${run.evalMeta.train_count} train / ${run.evalMeta.val_count} val stored, baseline scored. Next shows the baseline.`}
            nextLabel="Next: Baseline"
            nextHint="The baseline was scored at upload — step 3 explains what it means."
            onNext={() => setStep(3)}
            enabled
          />
        );
      }
      return (
        <WizardNextBar
          narration={`Run ${job.id} created — the skill is locked until the run ends. Next does nothing until training data is accepted.`}
          nextLabel="Next: Baseline"
          nextHint="Paste or upload your cases above; the gate answers with a fix-it list per issue."
          onNext={() => setStep(3)}
          enabled={false}
          disabledReason="Get training data accepted first — the form above shows exactly what the gate wants."
        />
      );
    }
    if (current === 3) {
      const s0 = run.evalMeta?.baseline_s0 ?? job?.baseline_s0 ?? null;
      return (
        <WizardNextBar
          narration={
            s0 === null
              ? `Run ${job?.id ?? ''} — no baseline yet; the baseline is scored when the eval set passes the gate.`
              : `Baseline shown (S0 ${Math.round((s0 as number) * 10000) / 100}%). Next starts the evolution loop.`
          }
          nextLabel="Next: Evolve"
          nextHint="The loop is user-initiated too: one button, and you can pause or cancel any time."
          onNext={() => setStep(4)}
          enabled={s0 !== null}
          disabledReason="The baseline arrives with the accepted data — finish step 2 first."
        />
      );
    }
    // Step 4: Review is reachable whenever the loop has been started.
    const started = !!job && job.status !== 'queued';
    return (
      <WizardNextBar
        narration={
          started
            ? `The loop is running run ${job?.id ?? ''} — pause, approve-early, or abort are wired above. Next opens the review tables.`
            : 'Start the loop to begin evolving; you can pause or cancel at any moment.'
        }
        nextLabel="Next: Review"
        nextHint="Every gate decision, the baseline against the final best, and the audited rollback."
        onNext={() => setStep(5)}
        enabled={started}
        disabledReason="Start the loop first — the button above does it in one click."
      />
    );
  };

  const renderStepPane = () => {
    switch (step) {
      case 1:
        return (
          <div className="space-y-4">
            <WizardStepSelect
              skills={skills.map((entry) =>
                trainedStateOverrides[entry.name] ? { ...entry, trained_state: trainedStateOverrides[entry.name] } : entry,
              )}
              selectedSlug={slug}
              loading={false}
              onSelectSkill={handleSelectSkill}
            />
            {nextBarFor(1)}
          </div>
        );
      case 2:
        return (
          <div className="space-y-4">
            {knownToken && job && <WizardTokenDisclosure token={knownToken} jobId={job.id} onAlert={onAlert} />}
            <WizardStepData
              job={job}
              evalMeta={run.evalMeta}
              startBusy={startBusy}
              startError={startError}
              onStartRun={() => void startRun()}
              uploadBusy={uploadBusy}
              uploadIssues={uploadIssues}
              uploadError={uploadError}
              onUploadEval={(filename, content) => void uploadEval(filename, content)}
            />
            {nextBarFor(2)}
          </div>
        );
      case 3:
        return (
          <div className="space-y-4">
            <WizardStepBaseline job={job} evalMeta={run.evalMeta} />
            {nextBarFor(3)}
          </div>
        );
      case 4:
        return (
          <div className="space-y-4">
            {job && (job.status === 'queued' || job.status === 'paused' || job.pause_requested) && (
              <WizardStepEvolveStart
                job={job}
                dispatchBusy={dispatchBusy}
                dispatchError={dispatchError}
                onStartLoop={(task) => void dispatchLoop(task)}
                resuming={job.status === 'paused'}
              />
            )}
            <WizardRunHeader
              skillTitle={skill?.title ?? job?.skill_slug ?? slug}
              skillVersion={skill?.version}
              job={job}
              loop={loop}
              plateauCount={run.plateauCount}
              plateauLimit={run.plateauLimit}
              maxIterations={run.maxIterations}
              elapsedMs={elapsedMs}
              controlBusy={controlBusy}
              onPause={handlePause}
              onAbort={handleAbort}
              onApprove={handleApprove}
              onRefresh={() => void run.refresh()}
            />

            {run.error && (
              <div data-testid="wizard-run-error" className="rounded-2xl border border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400 text-xs font-semibold p-4">
                {run.error} — the last known state stays on screen; use the refresh button once the server answers.
              </div>
            )}

            <WizardScoreStrip
              iterations={iterations}
              currentIteration={currentIteration}
              currentPhase={loop?.current_phase}
            />

            <div className="space-y-3" data-testid="wizard-iteration-feed">
              {run.loading && iterations.length === 0 && (
                <p className="text-xs text-themeTextMuted">Loading the run state…</p>
              )}
              {!run.loading && !job && (
                <p className="text-xs text-themeTextMuted">
                  Nothing to follow yet — start a run from the Data step.
                </p>
              )}
              {iterations.map((rec) => (
                <WizardIterationCard
                  key={rec.iteration}
                  record={rec}
                  isCurrent={rec.iteration === currentIteration}
                  loop={loop}
                  candidate={rec.candidate_id ? candidates[rec.candidate_id] : undefined}
                  candidateLoading={!!rec.candidate_id && !candidates[rec.candidate_id]}
                />
              ))}
            </div>

            <p className="text-[10px] text-themeTextMuted">
              Live view polls the run every 2 s and stops when the run ends. Server-Sent Events for
              per-iteration updates are a future story — the bus does not carry loop events yet.
            </p>
            {nextBarFor(4)}
          </div>
        );
      case 5:
        return (
          <WizardStepReview
            slug={slug}
            job={job}
            evalMeta={run.evalMeta}
            iterations={iterations}
            trainedState={trainedState}
            onBack={() => setStep(4)}
            onRetrain={handleRetrain}
            onTrainedStateUpdate={handleTrainedStateUpdate}
            onAlert={onAlert}
          />
        );
      case 6:
        return <WizardStepReport jobId={job?.id ?? null} onBack={() => setStep(4)} onNavigate={onNavigate} />;
    }
  };

  return (
    <div ref={containerRef} className="flex-1 h-screen flex overflow-hidden min-w-0 bg-themeBgPrimary">
      {/* Left rail: the six-step stepper, minimizable like the app sidebar */}
      <div
        className={`no-print transition-all duration-300 ease-in-out flex-shrink-0 relative z-30 ${
          railMinimized ? 'w-0' : ''
        }`}
        style={{ width: railMinimized ? 0 : `${splitPercentage}%`, minWidth: railMinimized ? 0 : 220, maxWidth: '80%' }}
      >
        <div className="h-full overflow-y-auto border-r border-themeBorder bg-themeBgSecondary/40">
          <div className="p-4 space-y-3 border-b border-themeBorder">
            <button
              type="button"
              // navigateTo receives the bare slug and builds /articles/<slug> —
              // the exact route the Train button left: the skill's article viewer.
              onClick={() => onNavigate(slug)}
              className="inline-flex items-center gap-1.5 text-[11px] font-bold text-themeTextMuted hover:text-themeAccent transition-colors cursor-pointer"
            >
              <ArrowLeft size={12} />
              Back to skill
            </button>
            {skill ? (
              <div className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <div className="p-1.5 rounded-lg bg-themeAccentBg text-themeAccent">
                    <Wrench size={13} />
                  </div>
                  <h2 className="text-sm font-black text-themeTextPrimary truncate">{skill.title}</h2>
                </div>
                <div className="flex items-center gap-2">
                  <TrainedBadge state={trainedState} />
                  <span className="text-[10px] text-themeTextMuted">v{skill.version}</span>
                </div>
              </div>
            ) : (
              <p className="text-[11px] text-themeTextMuted">Skill “{slug}” is not in the registry…</p>
            )}
          </div>
          <WizardStepper
            steps={steps}
            currentStep={step}
            onSelectStep={(next) => setStep(next)}
          />
        </div>

        {/* Border-aligned slide toggle, same shape as the app sidebar's */}
        <button
          onClick={() => setRailMinimized((prev) => !prev)}
          className="absolute top-1/2 -right-3.5 transform -translate-y-1/2 z-50 w-7 h-7 rounded-full bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 text-slate-500 hover:text-themeAccent dark:hover:text-themeAccent shadow-md hover:scale-110 active:scale-95 transition-all duration-200 cursor-pointer flex items-center justify-center no-print"
          title={railMinimized ? 'Expand steps' : 'Minimize steps'}
          aria-label={railMinimized ? 'Expand wizard steps' : 'Minimize wizard steps'}
        >
          {railMinimized ? <ChevronRight size={14} /> : <ChevronLeft size={14} />}
        </button>
      </div>

      {/* Drag divider, the editor's split-resize pattern */}
      {!railMinimized && (
        <div
          onMouseDown={startResizing}
          role="separator"
          aria-orientation="vertical"
          className="w-1.5 cursor-col-resize bg-themeBgSecondary hover:bg-themeAccent/50 active:bg-themeAccent/80 transition-colors flex-shrink-0 z-10"
        />
      )}
      {isDragging && <div className="fixed inset-0 z-50 cursor-col-resize select-none" />}

      {/* Right detail pane */}
      <div className="flex-1 overflow-y-auto h-full min-w-0">
        <div className="max-w-3xl mx-auto px-6 py-8 sm:px-10 animate-fade-in" key={`step-${step}`}>
          {renderStepPane()}
        </div>
      </div>
    </div>
  );
};
