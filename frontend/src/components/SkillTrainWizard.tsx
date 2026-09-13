import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { EvolutionJob, SkillRegistryEntry, SkillTrainedState, SkillTrainedStateEntry } from '../types';
import { useEvolutionRun, isEvolutionJobTerminal } from '../hooks/useEvolutionRun';
import { useSplitPane } from '../hooks/useSplitPane';
import { pickFollowedJob } from './wizardChips';
import { ChevronLeft, ChevronRight, ArrowLeft, Wrench } from 'lucide-react';
import { WizardStepper, type WizardStepId, type WizardStepInfo } from './WizardStepper';
import { WizardRunHeader, type WizardControl } from './WizardRunHeader';
import { WizardScoreStrip } from './WizardScoreStrip';
import { WizardIterationCard } from './WizardIterationCard';
import { WizardStepSelect, WizardStepData, WizardStepBaseline, WizardStepReview, WizardStepReport } from './WizardStepPanels';
import { TrainedBadge } from './TrainedBadge';

/**
 * The WikiSkill evolution wizard (story 08, Phase D/D1 + D2; story 09, Phase
 * D3): a full page at /skills/<slug>/train with the six-step rail on the left
 * and the detail pane on the right, reusing the app's minimizable-sidebar and
 * split-resize patterns and only the theme tokens.
 *
 * Steps 1-3 render server state (picker, eval upload, baseline). Step 4 is the
 * live loop view; step 5 the Review tables with the audited Rollback control,
 * and step 6 the run's auto-generated Result report. The run header and the
 * iteration feed poll the story-08 REST seam, and the human controls call the
 * audited story-07/09 wrappers through it.
 */

export type ToastFn = (type: 'success' | 'error', text: string) => void;

interface SkillTrainWizardProps {
  slug: string;
  skills: SkillRegistryEntry[];
  onSkillsRefresh: () => Promise<void> | void;
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
  // up without losing the followed one.
  const [followedJobId, setFollowedJobId] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    const discover = async () => {
      try {
        const res = await fetch(`/api/evolution/jobs?skill=${encodeURIComponent(slug)}`);
        if (!res.ok) return;
        const jobs: EvolutionJob[] = await res.json();
        if (cancelled) return;
        const picked = pickFollowedJob(Array.isArray(jobs) ? jobs : []);
        setFollowedJobId(picked?.id ?? null);
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
  // the jump again.
  const autoJumpedFor = useRef<string | null>(null);
  useEffect(() => {
    // A live run opening the wizard jumps it straight to the Evolve step —
    // the operator came here to watch the run, not to click through.
    if (job && autoJumpedFor.current !== job.id) {
      autoJumpedFor.current = job.id;
      setStep(4);
    }
  }, [job]);

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
      { id: 1, title: 'Select', hint: 'Pick the skill to evolve', status: 'done' as const },
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
  }, [job, run.evalMeta, iterations.length]);

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

  const renderStepPane = () => {
    switch (step) {
      case 1:
        return (
          <WizardStepSelect
            skills={skills.map((entry) =>
              trainedStateOverrides[entry.name] ? { ...entry, trained_state: trainedStateOverrides[entry.name] } : entry,
            )}
            selectedSlug={slug}
            loading={false}
            onSelectSkill={handleSelectSkill}
          />
        );
      case 2:
        return <WizardStepData job={job} evalMeta={run.evalMeta} />;
      case 3:
        return <WizardStepBaseline job={job} evalMeta={run.evalMeta} />;
      case 4:
        return (
          <div className="space-y-4">
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
                  Nothing to follow yet — the run header above explains how a run starts.
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
              onClick={() => onNavigate(`/articles/${slug}`)}
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
