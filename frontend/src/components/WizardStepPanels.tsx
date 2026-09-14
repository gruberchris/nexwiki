import React, { useCallback, useEffect, useState } from 'react';
import type {
  EvalMeta,
  EvolutionJob,
  IterationRecord,
  SkillAuditTrailView,
  SkillRegistryEntry,
  SkillResultReportView,
  SkillTrainedState,
  SkillTrainedStateEntry,
  WizardDataFormatId,
  WizardEvalIssuesView,
} from '../types';
import { isRealTimestamp, WIZARD_DATA_FORMATS, wizardFormatFilename } from '../types';
import type { ToastFn } from './SkillTrainWizard';
import {
  Database,
  FlaskConical,
  Gauge,
  UploadCloud,
  ArrowRight,
  CheckCircle2,
  XCircle,
  Sparkles,
  ClipboardList,
  FileText,
  Undo2,
  RotateCcw,
  Loader2,
  RefreshCw,
  ExternalLink,
  Hourglass,
  Play,
  Rocket,
  ShieldQuestion,
} from 'lucide-react';
import { TrainedBadge } from './TrainedBadge';
import {
  auditOutcomeChipClass,
  auditOutcomeLabel,
  iterationChipClass,
  iterationOutcomeLabel,
  loopOutcomeChipClass,
  outcomeLabel,
} from './wizardChips';

/**
 * The wizard's per-step detail panes (stories 08–09). Every pane renders
 * server state — the picker rows come from the registry's derived trained
 * state, the data and baseline panes read the job record and the stored eval
 * metadata, the review pane reads the audit trail, the loop records, and the
 * derived trained state, and the report pane reads the run's auto-generated
 * Result report. Nothing here fakes progress: a step whose server state has
 * not arrived yet says so.
 */

export const WizardPane: React.FC<{ title: string; subtitle?: string; children?: React.ReactNode }> = ({
  title,
  subtitle,
  children,
}) => (
  <div className="space-y-4">
    <div>
      <h2 className="text-lg font-black text-themeTextPrimary">{title}</h2>
      {subtitle && <p className="text-xs text-themeTextMuted mt-1 leading-relaxed max-w-xl">{subtitle}</p>}
    </div>
    {children}
  </div>
);

const HintCard: React.FC<{ icon: React.ReactNode; title: string; lines: React.ReactNode[] }> = ({ icon, title, lines }) => (
  <div className="rounded-2xl border border-dashed border-themeBorder bg-themeBgSecondary/40 p-5 flex gap-3">
    <div className="p-2 rounded-xl bg-themeAccentBg text-themeAccent shrink-0 h-9 w-9 flex items-center justify-center">{icon}</div>
    <div className="min-w-0">
      <h4 className="text-xs font-black text-themeTextSecondary">{title}</h4>
      <ul className="mt-1.5 space-y-1">
        {lines.map((line, i) => (
          <li key={i} className="text-[11px] text-themeTextMuted leading-relaxed flex gap-1.5">
            <span className="text-themeAccent">›</span>
            <span>{line}</span>
          </li>
        ))}
      </ul>
    </div>
  </div>
);

// --- Step 1: Select -----------------------------------------------------------------

interface WizardStepSelectProps {
  skills: SkillRegistryEntry[];
  selectedSlug: string;
  loading: boolean;
  onSelectSkill: (slug: string) => void;
}

export const WizardStepSelect: React.FC<WizardStepSelectProps> = ({ skills, selectedSlug, loading, onSelectSkill }) => (
  <WizardPane
    title="1 · Select a skill to evolve"
    subtitle="Every registered AI-Agent-Skill with its derived trained state. Untrained and stale skills are the ones the loop can improve; training a fresh marker replaces the previous one."
  >
    {loading && <p className="text-xs text-themeTextMuted">Loading the skill registry…</p>}
    {!loading && skills.length === 0 && (
      <HintCard
        icon={<Sparkles size={16} />}
        title="No skills registered yet"
        lines={["Create a Custom AI Skill page — the wizard trains AI-Agent-Skill documents."]}
      />
    )}
    {!loading && skills.length > 0 && (
      <ul className="grid grid-cols-1 md:grid-cols-2 gap-3">
        {skills.map((skill) => {
          const selected = skill.name === selectedSlug;
          return (
            <li key={skill.name}>
              <button
                type="button"
                onClick={() => onSelectSkill(skill.name)}
                data-testid={`wizard-picker-${skill.name}`}
                aria-pressed={selected}
                className={`w-full text-left p-4 rounded-2xl border bg-themeBgSecondary/60 transition-all cursor-pointer hover:border-themeAccent/40 hover:scale-[1.01] active:scale-100 ${
                  selected ? 'border-themeAccent/60 ring-2 ring-themeAccent/20' : 'border-themeBorder'
                }`}
              >
                <div className="flex items-center gap-2 justify-between">
                  <span className="text-xs font-bold text-themeTextPrimary truncate">{skill.title}</span>
                  <TrainedBadge state={skill.trained_state} />
                </div>
                <p className="mt-1 text-[10px] text-themeTextMuted">
                  v{skill.version} · {skill.description ? skill.description.slice(0, 90) : 'No description'}
                </p>
              </button>
            </li>
          );
        })}
      </ul>
    )}
  </WizardPane>
);

// --- Step 2: Data -------------------------------------------------------------------

interface WizardStepDataProps {
  job: EvolutionJob | null;
  evalMeta: EvalMeta | null;
  /** True while POST /train/start is in flight. */
  startBusy: boolean;
  /** The start-run failure, already carrying the server's fix. */
  startError: string | null;
  /** Fires POST /api/skills/{slug}/train/start. */
  onStartRun: () => void;
  /** True while POST /eval is in flight. */
  uploadBusy: boolean;
  /** The story-05 gate's per-issue fix-it list for the last refused upload. */
  uploadIssues: WizardEvalIssuesView | null;
  /** Any other eval-upload failure (network, terminal job), with its fix. */
  uploadError: string | null;
  /** Fires POST /api/evolution/jobs/{id}/eval with filename + content. */
  onUploadEval: (filename: string, content: string) => void;
}

/**
 * The guided data-entry form: paste (JSONL/JSON/CSV/text) or a file upload,
 * both feeding the same filename+content payload the story-05 gate consumes.
 * Local state only — a refused upload leaves the text on screen so the
 * operator can fix the listed issues instead of retyping.
 */
const WizardDataForm: React.FC<{ uploadBusy: boolean; onSubmit: (filename: string, content: string) => void }> = ({ uploadBusy, onSubmit }) => {
  const [format, setFormat] = useState<WizardDataFormatId>('jsonl');
  const [content, setContent] = useState('');
  const [fileError, setFileError] = useState<string | null>(null);

  const handleFile = useCallback(async (file: File | undefined) => {
    if (!file) return;
    setFileError(null);
    if (file.size > 2 * 1024 * 1024) {
      setFileError(`"${file.name}" is ${(file.size / (1024 * 1024)).toFixed(1)} MB — the format gate accepts at most 2 MB per upload; trim or split the set.`);
      return;
    }
    try {
      const text = await file.text();
      setContent(text);
      // The file's own extension decides the format: it is what the gate routes on.
      const ext = file.name.slice(file.name.lastIndexOf('.')).toLowerCase() || '.jsonl';
      const match = WIZARD_DATA_FORMATS.find((entry) => entry.ext === ext);
      if (match) setFormat(match.id);
      else setFileError(`Accepted file types are ${WIZARD_DATA_FORMATS.map((entry) => entry.ext).join(', ')} — "${file.name}" will be refused.`);
    } catch {
      setFileError(`Could not read "${file.name}" — pick the file again.`);
    }
  }, []);

  const canSubmit = content.trim().length > 0 && !uploadBusy;
  return (
    <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 space-y-3" data-testid="wizard-data-form">
      <div className="flex flex-wrap gap-1.5">
        {WIZARD_DATA_FORMATS.map((entry) => (
          <button
            key={entry.id}
            type="button"
            onClick={() => setFormat(entry.id)}
            aria-pressed={format === entry.id}
            className={`px-2.5 py-1 rounded-lg text-[10px] font-bold border transition-colors cursor-pointer ${
              format === entry.id ? 'bg-themeAccentBg text-themeAccent border-themeAccent/50' : 'bg-themeBgPrimary text-themeTextMuted border-themeBorder hover:border-themeAccent/40'
            }`}
          >
            {entry.ext}
          </button>
        ))}
        <p className="text-[10px] text-themeTextMuted leading-relaxed min-w-0 flex-1 basis-full">{WIZARD_DATA_FORMATS.find((entry) => entry.id === format)?.label}</p>
      </div>
      <textarea
        data-testid="wizard-data-textarea"
        value={content}
        onChange={(e) => setContent(e.target.value)}
        placeholder={format === 'text' ? 'How do I round a value? ||| Math.round(n)' : '{"input":"How do I round a value?","expected":"Math.round(n)"}'}
        rows={8}
        aria-label="Training cases"
        className="w-full rounded-xl border border-themeBorder bg-themeBgPrimary px-3 py-2 text-xs text-themeTextPrimary font-mono placeholder:text-themeTextMuted/70 focus:outline-none focus:border-themeAccent/50"
      />
      <div className="flex flex-wrap items-center gap-2">
        <label className="inline-flex items-center gap-1.5 py-2 px-3 rounded-xl border border-themeBorder bg-themeBgPrimary text-themeTextMuted hover:text-themeAccent hover:border-themeAccent/40 font-semibold text-xs transition-colors cursor-pointer">
          <UploadCloud size={12} />
          <span>Upload a file</span>
          <input
            type="file"
            className="hidden"
            accept={WIZARD_DATA_FORMATS.map((entry) => entry.ext).join(',')}
            onChange={(e) => {
              void handleFile(e.target.files?.[0]);
              e.target.value = '';
            }}
          />
        </label>
        <span className="text-[10px] text-themeTextMuted">or paste above — one case per line, {wizardFormatFilename(format).slice(6)} format</span>
        <button
          type="button"
          onClick={() => onSubmit(wizardFormatFilename(format), content)}
          disabled={!canSubmit}
          data-testid="wizard-submit-data"
          className="ml-auto inline-flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-themeAccent/40 bg-themeAccentBg text-themeAccent hover:border-themeAccent font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
        >
          {uploadBusy ? <Loader2 size={12} className="animate-spin" /> : <ShieldQuestion size={12} />}
          <span>{uploadBusy ? 'Validating…' : 'Submit training data'}</span>
        </button>
      </div>
      {uploadBusy && <p className="text-[10px] text-themeTextMuted">Running the format gate — parsing, split counts, dedupe, leakage, and the secret scan; nothing is stored unless it passes.</p>}
      {fileError && <p data-testid="wizard-data-file-error" className="text-[10px] text-amber-600 dark:text-amber-400 leading-relaxed">{fileError}</p>}
    </div>
  );
};

// The gate refusal as a fix-it list: one line per issue, verbatim from the gate.
const WizardIssueList: React.FC<{ issues: WizardEvalIssuesView }> = ({ issues }) => (
  <div data-testid="wizard-data-issues" className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-4 space-y-2">
    <h4 className="text-xs font-black text-rose-600 dark:text-rose-400 flex items-center gap-1.5">
      <XCircle size={12} />
      The format gate refused this set — nothing was stored
    </h4>
    <p className="text-[10px] text-rose-600/80 dark:text-rose-400/80 leading-relaxed">
      {issues.parsed} case{issues.parsed === 1 ? '' : 's'} parsed
      {issues.split_mode ? ` · ${issues.split_mode} splits would hold ${issues.train_count} train / ${issues.val_count} val` : ''} · fix each issue below and submit again (your text is still on screen).
    </p>
    <ul className="space-y-1.5">
      {issues.issues.map((issue, i) => (
        <li key={i} data-testid="wizard-data-issue" className="text-[11px] text-rose-600 dark:text-rose-400 leading-relaxed flex gap-1.5">
          <span className="shrink-0">›</span>
          <span className="font-mono break-all">{issue}</span>
        </li>
      ))}
    </ul>
  </div>
);

export const WizardStepData: React.FC<WizardStepDataProps> = ({
  job,
  evalMeta,
  startBusy,
  startError,
  onStartRun,
  uploadBusy,
  uploadIssues,
  uploadError,
  onUploadEval,
}) => {
  if (!job) {
    return (
      <WizardPane title="2 · Training data" subtitle="The run's validated {input, expected} cases, stored under the job after the format gate accepts them.">
        <HintCard
          icon={<Database size={16} />}
          title="No evolution run yet"
          lines={[
            'Starting the run locks the skill until it ends — one run per skill, at most ~90 minutes.',
            'Then paste or upload your training cases right here; the wizard walks the format gate with you.',
          ]}
        />
        <button
          type="button"
          onClick={onStartRun}
          disabled={startBusy}
          data-testid="wizard-start-run-button"
          className="inline-flex items-center gap-1.5 py-2.5 px-4 rounded-xl border border-themeAccent/40 bg-themeAccentBg text-themeAccent hover:border-themeAccent font-bold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
        >
          {startBusy ? <Loader2 size={13} className="animate-spin" /> : <Rocket size={13} />}
          <span>{startBusy ? 'Creating the run…' : 'Start training run'}</span>
        </button>
        {startBusy && <p className="text-[10px] text-themeTextMuted">Minting the run and locking the skill — the run ID appears here as soon as the server answers.</p>}
        {startError && (
          <div data-testid="wizard-start-error" className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-4 space-y-1.5">
            <p className="text-[11px] text-rose-600 dark:text-rose-400 leading-relaxed">{startError}</p>
            <p className="text-[10px] text-rose-600/80 dark:text-rose-400/80 leading-relaxed">If an active run already exists, open the Evolve step — the wizard follows it and its controls from there.</p>
          </div>
        )}
      </WizardPane>
    );
  }

  const accepted = !!evalMeta;
  return (
    <WizardPane
      title="2 · Training data"
      subtitle={accepted ? 'What the format gate accepted and stored for this run.' : 'Paste or upload the training cases — the format gate validates them before anything is stored.'}
    >
      {accepted && evalMeta && (
        <div className="space-y-3" data-testid="wizard-data-accepted">
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
            {[
              { label: 'Train cases', value: evalMeta.train_count },
              { label: 'Val cases', value: evalMeta.val_count },
              { label: 'Split mode', value: evalMeta.split_mode },
              { label: 'Scorer', value: evalMeta.scorer_version },
            ].map((stat) => (
              <div key={stat.label} className="p-4 rounded-2xl border border-themeBorder bg-themeBgSecondary/60">
                <p className="text-[10px] font-bold uppercase tracking-wider text-themeTextMuted">{stat.label}</p>
                <p className="mt-1 text-base font-black text-themeTextPrimary">{stat.value}</p>
              </div>
            ))}
          </div>
          <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 space-y-2">
            <p className="text-[11px] text-themeTextSecondary">
              <span className="font-bold">{evalMeta.filename}</span> · uploaded{' '}
              {new Date(evalMeta.uploaded_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </p>
            <p className="text-[10px] text-themeTextMuted font-mono break-all">
              eval hash {(evalMeta.eval_hash || '').slice(0, 24)}…
            </p>
            <p data-testid="wizard-data-s0-preview" className="text-[10px] text-themeTextMuted leading-relaxed">
              Scored at upload: S0 {Math.round((evalMeta.baseline_s0 ?? 0) * 10000) / 100}% on the val split
              {evalMeta.dry_run.length > 0 ? ` · dry run scored ${evalMeta.dry_run.length} sample${evalMeta.dry_run.length === 1 ? '' : 's'} (full preview on the Baseline step)` : ''}.
            </p>
            <p className="text-[10px] text-themeTextMuted leading-relaxed">
              The eval hash is what the trained marker will be checked against later: re-uploading after
              training is exactly what marks the skill stale.
            </p>
          </div>
        </div>
      )}

      {!accepted && (
        <>
          {uploadIssues && <WizardIssueList issues={uploadIssues} />}
          {uploadError && (
            <div data-testid="wizard-upload-error" className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-4 space-y-1.5">
              <p className="text-[11px] text-rose-600 dark:text-rose-400 leading-relaxed">{uploadError}</p>
              <p className="text-[10px] text-rose-600/80 dark:text-rose-400/80 leading-relaxed">If the run just ended, start a fresh run once the skill lock releases.</p>
            </div>
          )}
          <WizardDataForm uploadBusy={uploadBusy} onSubmit={onUploadEval} />
          <HintCard
            icon={<Database size={16} />}
            title="What the gate enforces (story 05, unchanged)"
            lines={[
              'Cases shaped {input, expected [, scorer]} — CSV, JSONL, JSON, or text lines "input ||| expected".',
              'Refuses secrets/PII shapes, duplicate cases, and identical inputs across train/val (leakage) — with one fix-it line per issue.',
              'A re-upload replaces the splits wholesale while the run is live.',
            ]}
          />
        </>
      )}

      {accepted && (
        <details data-testid="wizard-data-replace">
          <summary className="text-[11px] font-bold text-themeAccent cursor-pointer">Replace the training data (re-uploads replace the splits)</summary>
          <div className="mt-3 space-y-3">
            {uploadIssues && <WizardIssueList issues={uploadIssues} />}
            {uploadError && <p data-testid="wizard-upload-error" className="text-[10px] text-rose-600 dark:text-rose-400 leading-relaxed">{uploadError}</p>}
            <WizardDataForm uploadBusy={uploadBusy} onSubmit={onUploadEval} />
          </div>
        </details>
      )}
    </WizardPane>
  );
};

// --- Step 3: Baseline ---------------------------------------------------------------

interface WizardStepBaselineProps {
  job: EvolutionJob | null;
  evalMeta: EvalMeta | null;
}

export const WizardStepBaseline: React.FC<WizardStepBaselineProps> = ({ job, evalMeta }) => {
  if (!job) {
    return (
      <WizardPane title="3 · Baseline" subtitle="The current skill's score on the validation split — the R_best every candidate must beat.">
        <HintCard
          icon={<Gauge size={16} />}
          title="No evolution run yet"
          lines={['The baseline is scored when the eval set is accepted; dispatch a run first.']}
        />
      </WizardPane>
    );
  }
  const s0 = evalMeta?.baseline_s0 ?? job.baseline_s0 ?? null;
  if (s0 === null) {
    return (
      <WizardPane title="3 · Baseline" subtitle="Waiting for the eval set that the baseline is scored against.">
        <HintCard
          icon={<Gauge size={16} />}
          title="No baseline yet"
          lines={['The S0 baseline and the 5-sample dry run are computed when the eval upload passes the format gate.']}
        />
      </WizardPane>
    );
  }
  const percent = Math.round(s0 * 10000) / 100;
  return (
    <WizardPane title="3 · Baseline" subtitle="S0 — the live skill's score on the validation split. Every accepted iteration must beat R_best, which starts here.">
      <p data-testid="wizard-baseline-note" className="text-[11px] text-themeTextSecondary leading-relaxed rounded-2xl border border-dashed border-themeBorder bg-themeBgSecondary/40 p-4">
        Already computed: the format gate scored this live skill against the val split the moment it accepted
        your data (step 2) — the numbers below came with that upload, so there is nothing to rerun. Next
        starts the evolution loop, which must beat R_best from here on.
      </p>
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-5">
        <div className="flex items-baseline gap-2">
          <span className="text-3xl font-black text-themeAccent">{percent}%</span>
          <span className="text-[11px] text-themeTextMuted">S0 · {evalMeta?.scorer_version ?? job.baseline_scorer} scorer</span>
        </div>
        <div className="mt-3 h-2 rounded-full bg-themeBgPrimary border border-themeBorder overflow-hidden">
          <div
            data-testid="wizard-baseline-bar"
            className="h-full bg-themeAccent"
            style={{ width: `${Math.min(100, Math.max(2, percent))}%` }}
          />
        </div>
      </div>

      {evalMeta && evalMeta.dry_run.length > 0 && (
        <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4">
          <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
            <FlaskConical size={12} className="text-themeAccent" />
            Dry run ({evalMeta.dry_run.length} val samples)
          </h4>
          <ul className="mt-3 space-y-2">
            {evalMeta.dry_run.map((sample) => (
              <li key={sample.index} className="flex items-start gap-2 text-[11px]">
                {sample.pass ? (
                  <CheckCircle2 size={12} className="text-emerald-500 shrink-0 mt-0.5" />
                ) : (
                  <XCircle size={12} className="text-rose-400 shrink-0 mt-0.5" />
                )}
                <span className="text-themeTextMuted font-mono break-all">
                  [{sample.index}] {sample.input_preview} → {sample.expected_preview}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {evalMeta && (
        <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4">
          <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
            <Gauge size={12} className="text-themeAccent" />
            Cost estimate (fields only — budget enforcement lands in Phase F3)
          </h4>
          <p className="mt-1.5 text-[11px] text-themeTextMuted leading-relaxed">
            {evalMeta.estimate.estimated_val_cases} val cases per gate · ≈${evalMeta.estimate.estimated_cost_usd} per
            full-gate evaluation {evalMeta.estimate.note ? `· ${evalMeta.estimate.note}` : ''}
          </p>
        </div>
      )}
    </WizardPane>
  );
};

// --- Step 4: Evolve — the start-the-loop block (story 13) ----------------------------

interface WizardStepEvolveStartProps {
  job: EvolutionJob;
  /** True while POST /dispatch is in flight. */
  dispatchBusy: boolean;
  /** The dispatch failure, already carrying the server's fix. */
  dispatchError: string | null;
  /** Fires POST /api/evolution/jobs/{id}/dispatch with the task line. */
  onStartLoop: (task: string) => void;
  /** True when the job is parked: the button resumes the run. */
  resuming?: boolean;
}

/**
 * The user-initiated start of the loop (story 13): one button, a plain-language
 * line of exactly what will happen, and the optional task line the harness
 * follows. Shown above the live view while the job is queued or parked; once
 * the loop drives, the live view takes over.
 */
export const WizardStepEvolveStart: React.FC<WizardStepEvolveStartProps> = ({ job, dispatchBusy, dispatchError, onStartLoop, resuming = false }) => {
  const [task, setTask] = useState('');
  return (
    <section data-testid="wizard-evolve-start" className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-5 space-y-3">
      <h3 className="text-sm font-black text-themeTextPrimary">{resuming ? 'Resume the run' : 'Start the loop'}</h3>
      <p className="text-[11px] text-themeTextMuted leading-relaxed">
        {resuming ? (
          <>The run is parked at its recorded step. Resuming re-queues it from the checkpoint — the loop continues exactly where it left off.</>
        ) : (
          <>The server drives the run headless, itself: up to {job.max_iterations || 12} iterations inside a ~90-minute cap, with a server-side gate deciding every proposal. You can pause or cancel it at any moment — the view below updates as each step lands, so you never wait blind.</>
        )}
      </p>
      <input
        type="text"
        value={task}
        onChange={(e) => setTask(e.target.value)}
        placeholder="Instruction for the run (optional) — e.g. 'Use the eval set to improve the troubleshooting section'"
        data-testid="wizard-dispatch-task"
        aria-label="Instruction for the run"
        className="w-full rounded-xl border border-themeBorder bg-themeBgPrimary px-3 py-2 text-xs text-themeTextPrimary placeholder:text-themeTextMuted/70 focus:outline-none focus:border-themeAccent/50"
      />
      <button
        type="button"
        onClick={() => onStartLoop(task)}
        disabled={dispatchBusy}
        data-testid="wizard-start-loop-button"
        className="inline-flex items-center gap-1.5 py-2.5 px-4 rounded-xl border border-themeAccent/40 bg-themeAccentBg text-themeAccent hover:border-themeAccent font-bold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
      >
        {dispatchBusy ? <Loader2 size={13} className="animate-spin" /> : <Play size={13} />}
        <span>{dispatchBusy ? 'Starting the loop…' : resuming ? 'Resume the run' : 'Start the loop'}</span>
      </button>
      {dispatchBusy && <p className="text-[10px] text-themeTextMuted">Claiming the job and opening the loop — the live view below takes over within a couple of seconds.</p>}
      {dispatchError && (
        <div data-testid="wizard-dispatch-error" className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-4 space-y-1.5">
          <p className="text-[11px] text-rose-600 dark:text-rose-400 leading-relaxed">{dispatchError}</p>
          <p className="text-[10px] text-rose-600/80 dark:text-rose-400/80 leading-relaxed">If a harness on this machine already holds the job, the live view here follows it all the same.</p>
        </div>
      )}
    </section>
  );
};

// --- The shared Next bar (story 13) ---------------------------------------------------

export interface WizardNextBarProps {
  /** One-line narration of what the previous action did and what Next does. */
  narration: string;
  nextLabel: string;
  nextHint?: string;
  onNext: () => void;
  enabled: boolean;
  /** Disabled-state explanation — always better than a mute button. */
  disabledReason?: string;
}

/**
 * The user-driven walk (story 13): every step ends with one line of narration
 * and the Next action. Next is enabled exactly when the step's work is done;
 * a disabled Next always says why, so no step is ever a dead end.
 */
export const WizardNextBar: React.FC<WizardNextBarProps> = ({ narration, nextLabel, nextHint, onNext, enabled, disabledReason }) => (
  <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 space-y-2" data-testid="wizard-next-bar">
    <p data-testid="wizard-next-narration" className="text-[11px] text-themeTextSecondary leading-relaxed">
      {narration}
    </p>
    <div className="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onClick={onNext}
        disabled={!enabled}
        data-testid="wizard-next-button"
        className="inline-flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-themeAccent/40 bg-themeAccentBg text-themeAccent hover:border-themeAccent font-bold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
      >
        <ArrowRight size={12} />
        <span>{nextLabel}</span>
      </button>
      {enabled && nextHint && <span className="text-[10px] text-themeTextMuted leading-relaxed">{nextHint}</span>}
      {!enabled && disabledReason && <span data-testid="wizard-next-disabled-reason" className="text-[10px] text-amber-600 dark:text-amber-400 leading-relaxed">{disabledReason}</span>}
    </div>
  </div>
);

// --- Steps 5 & 6: Review + Result report (story 09) ----------------------------------

const pct = (v: number): string => `${Math.round(v * 10000) / 100}%`;
const score4 = (v: number): string => v.toFixed(4);

const Th: React.FC<React.ThHTMLAttributes<HTMLTableCellElement>> = ({ children, className = '', ...rest }) => (
  <th {...rest} className={`px-2 py-1.5 text-left text-[10px] font-bold uppercase tracking-wider text-themeTextMuted ${className}`}>
    {children}
  </th>
);
const Td: React.FC<React.TdHTMLAttributes<HTMLTableCellElement>> = ({ children, className = '', ...rest }) => (
  <td {...rest} className={`px-2 py-1.5 text-[11px] text-themeTextSecondary align-top ${className}`}>
    {children}
  </td>
);

interface WizardStepReviewProps {
  slug: string;
  job: EvolutionJob | null;
  evalMeta: EvalMeta | null;
  /** The run's per-iteration records, already polled by the wizard's live view. */
  iterations: IterationRecord[];
  /** The skill's derived trained state — the staleness banner's server truth. */
  trainedState: SkillTrainedState | null;
  onBack: () => void;
  /** Restart the wizard flow at step 1 (the picker). */
  onRetrain: () => void;
  /** Delivers the refreshed derived trained state a rollback returns. */
  onTrainedStateUpdate: (state: SkillTrainedStateEntry) => void;
  onAlert: ToastFn;
}

/**
 * The Review step (story 09): the run's outcome read back from the records —
 * the baseline-vs-final table (S0 from the eval metadata, final R_best from
 * the skill's audit trail), the iteration history from the loop endpoint, the
 * eval hash + counts, the derived trained state with its staleness reason, the
 * honest limitations, and the audited Rollback control. The Re-train control
 * restarts the flow at the picker.
 */
export const WizardStepReview: React.FC<WizardStepReviewProps> = ({
  slug,
  job,
  evalMeta,
  iterations,
  trainedState,
  onBack,
  onRetrain,
  onTrainedStateUpdate,
  onAlert,
}) => {
  const [audit, setAudit] = useState<SkillAuditTrailView | null>(null);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [auditLoading, setAuditLoading] = useState(true);
  const [rollbackReason, setRollbackReason] = useState('');
  const [rollingBack, setRollingBack] = useState(false);

  const loadAudit = useCallback(async () => {
    setAuditLoading(true);
    try {
      const res = await fetch(`/api/skills/${encodeURIComponent(slug)}/audit`);
      if (!res.ok) {
        const errBody = await res.json().catch(() => ({}));
        throw new Error(errBody.error || `The audit trail request failed (${res.status})`);
      }
      setAudit((await res.json()) as SkillAuditTrailView);
      setAuditError(null);
    } catch (err: unknown) {
      setAuditError(err instanceof Error ? err.message : 'Failed to load the audit trail');
    } finally {
      setAuditLoading(false);
    }
  }, [slug]);

  useEffect(() => {
    // Deferred to the next macrotask like the other views' first poll: the
    // effect body stays free of synchronous setState calls.
    const kick = setTimeout(() => void loadAudit(), 0);
    return () => clearTimeout(kick);
  }, [loadAudit]);

  const handleRollback = useCallback(async () => {
    const reason = rollbackReason.trim();
    if (!reason || rollingBack) return;
    if (!window.confirm(`Roll back "${slug}" to its pre-training version? The trained marker is revoked with your reason and the training history stays readable.`)) return;
    setRollingBack(true);
    try {
      const res = await fetch(`/api/skills/${encodeURIComponent(slug)}/rollback`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason }),
      });
      if (!res.ok) {
        const errBody = await res.json().catch(() => ({}));
        throw new Error(errBody.error || `The rollback request failed (${res.status})`);
      }
      const state = (await res.json()) as SkillTrainedStateEntry;
      onTrainedStateUpdate(state);
      setRollbackReason('');
      onAlert('success', 'Rolled back — the skill body is restored to its pre-training version and the trained marker is revoked with your reason.');
      await loadAudit(); // the revocation is a real audit record now
    } catch (err: unknown) {
      onAlert('error', err instanceof Error ? err.message : 'The rollback request failed');
    } finally {
      setRollingBack(false);
    }
  }, [loadAudit, onAlert, onTrainedStateUpdate, rollbackReason, rollingBack, slug]);

  if (!job) {
    return (
      <WizardPane
        title="5 · Review"
        subtitle="The review tables over the run's records — the gate's every decision, the baseline against the final best, and the trained state as the server derives it."
      >
        <HintCard
          icon={<ClipboardList size={16} />}
          title="No evolution run yet"
          lines={['The review reads a run: go back to Step 2 and press Start training run, then return here.']}
        />
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-xs font-bold text-themeAccent hover:underline cursor-pointer"
        >
          <ArrowRight size={12} className="rotate-180" />
          Back to the live view
        </button>
      </WizardPane>
    );
  }

  const s0 = evalMeta?.baseline_s0 ?? job.baseline_s0 ?? null;
  const scorer = evalMeta?.scorer_version ?? job.baseline_scorer ?? 'v0';
  const rBest = audit ? audit.r_best : null;
  const patternSlugs = new Set<string>();
  for (const rec of iterations) {
    for (const p of rec.pattern_slugs ?? []) patternSlugs.add(p);
  }
  // A rollback withdraws the marker, so it needs one to exist — the server has
  // the final word (409 on a live run or a missing marker), and the control
  // surfaces that as an error toast.
  const canRollback = !!trainedState && trainedState.state !== 'untrained';

  return (
    <WizardPane
      title="5 · Review"
      subtitle="The run's outcome, read back from the records: every gate decision, the baseline against the final best, and the trained state as the server derives it right now."
    >
      {/* Trained state banner: the badge the picker shows, plus the stale reason */}
      <div
        data-testid="wizard-review-trained-state"
        className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 flex flex-wrap items-center gap-3"
      >
        <TrainedBadge state={trainedState} />
        {trainedState?.reason && (
          <p className="text-[11px] text-amber-600 dark:text-amber-400 leading-relaxed min-w-0 flex-1">{trainedState.reason}</p>
        )}
      </div>

      {/* Baseline vs final */}
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4" data-testid="wizard-review-baseline-final">
        <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
          <Gauge size={12} className="text-themeAccent" />
          Baseline vs final
        </h4>
        <table className="mt-3 w-full border-collapse">
          <thead>
            <tr>
              <Th>Split</Th>
              <Th>Baseline (S0)</Th>
              <Th>Final (R_best)</Th>
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-themeBorder">
              <Td className="font-semibold text-themeTextPrimary">val</Td>
              <Td>{s0 === null ? 'not scored (no eval set)' : `${pct(s0)} (S0, ${scorer} scorer)`}</Td>
              <Td data-testid="wizard-review-r-best">
                {auditLoading
                  ? 'loading the audit trail…'
                  : rBest === null
                    ? '— (audit trail unreachable)'
                    : `${pct(rBest)} (R_best)`}
              </Td>
            </tr>
            <tr className="border-t border-themeBorder">
              <Td>train</Td>
              <Td>— (the v0 scorer scores the val split only)</Td>
              <Td>—</Td>
            </tr>
          </tbody>
        </table>
        {s0 !== null && rBest !== null && (
          <p className="mt-2 text-[11px] text-themeTextMuted">
            Δ vs baseline: {(rBest - s0 >= 0 ? '+' : '') + (rBest - s0).toFixed(4)} (S0 {score4(s0)} → R_best {score4(rBest)}).
          </p>
        )}
        {auditError && (
          <p data-testid="wizard-review-audit-error" className="mt-2 text-[11px] text-amber-600 dark:text-amber-400 leading-relaxed">
            {auditError} — the final cell stays honest until the audit trail answers.
          </p>
        )}
      </div>

      {/* Iteration history (loop endpoint records, polled by the live view) */}
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4" data-testid="wizard-review-iterations">
        <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
          <FlaskConical size={12} className="text-themeAccent" />
          Iteration history
        </h4>
        {iterations.length === 0 ? (
          <p className="mt-2 text-[11px] text-themeTextMuted">No iterations recorded yet — the run has not started the loop.</p>
        ) : (
          <table className="mt-3 w-full border-collapse">
            <thead>
              <tr>
                <Th>#</Th>
                <Th>Candidate</Th>
                <Th>Val score</Th>
                <Th>R_best</Th>
                <Th>Outcome</Th>
              </tr>
            </thead>
            <tbody>
              {iterations.map((rec) => (
                <tr key={rec.iteration} className="border-t border-themeBorder">
                  <Td className="font-semibold text-themeTextPrimary">{rec.iteration}</Td>
                  <Td className="font-mono break-all">{rec.candidate_id || 'none proposed'}</Td>
                  <Td>
                    {score4(rec.val_score)} vs {score4(rec.r_best)}
                  </Td>
                  <Td>{score4(rec.r_best)}</Td>
                  <Td>
                    <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${iterationChipClass(rec.outcome)}`}>
                      {iterationOutcomeLabel(rec.outcome)}
                      {rec.result_version ? ` (promoted v${rec.result_version})` : ''}
                    </span>
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Eval hash + counts: the lineage the trained marker is checked against */}
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 space-y-2">
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
          {[
            { label: 'Train cases', value: evalMeta ? evalMeta.train_count : job.eval_train_count ?? '—' },
            { label: 'Val cases', value: evalMeta ? evalMeta.val_count : job.eval_val_count ?? '—' },
            { label: 'Scorer', value: scorer },
          ].map((stat) => (
            <div key={stat.label} className="rounded-xl border border-themeBorder bg-themeBgPrimary/60 p-3">
              <p className="text-[10px] font-bold uppercase tracking-wider text-themeTextMuted">{stat.label}</p>
              <p className="mt-1 text-sm font-black text-themeTextPrimary">{stat.value}</p>
            </div>
          ))}
        </div>
        <p className="text-[10px] text-themeTextMuted font-mono break-all">
          eval hash {(evalMeta?.eval_hash || job.eval_hash || '—').slice(0, 24)}…
        </p>
        <p className="text-[10px] text-themeTextMuted leading-relaxed">
          Re-uploading the eval set after training is exactly what marks the skill stale — the hash above is what the trained marker is checked against.
        </p>
      </div>

      {/* The skill's whole gate trail, across every run (story 03 records) */}
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4" data-testid="wizard-review-audit">
        <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
          <ClipboardList size={12} className="text-themeAccent" />
          Gate decisions (audit trail, all runs)
        </h4>
        {auditLoading && <p className="mt-2 text-[11px] text-themeTextMuted">Loading the audit trail…</p>}
        {!auditLoading && audit && audit.records.length === 0 && (
          <p className="mt-2 text-[11px] text-themeTextMuted">No gate decisions recorded for this skill yet.</p>
        )}
        {!auditLoading && audit && audit.records.length > 0 && (
          <table className="mt-3 w-full border-collapse">
            <thead>
              <tr>
                <Th>When</Th>
                <Th>Candidate</Th>
                <Th>Decision</Th>
                <Th>Decider</Th>
                <Th>Val score</Th>
                <Th>R_best</Th>
              </tr>
            </thead>
            <tbody>
              {audit.records.map((rec, i) => (
                <tr key={`${rec.timestamp}-${i}`} className="border-t border-themeBorder" title={rec.reason || undefined}>
                  <Td>{new Date(rec.timestamp).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</Td>
                  <Td className="font-mono break-all">{rec.candidate_id}</Td>
                  <Td>
                    <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${auditOutcomeChipClass(rec.outcome)}`}>
                      {auditOutcomeLabel(rec.outcome)}
                    </span>
                  </Td>
                  <Td>{rec.decider}</Td>
                  <Td>{score4(rec.validation_score)}</Td>
                  <Td>
                    {score4(rec.r_best_before)} → {score4(rec.r_best_after)}
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Limitations, stated where the numbers are read */}
      <div className="rounded-2xl border border-dashed border-themeBorder bg-themeBgSecondary/40 p-4">
        <h4 className="text-xs font-black text-themeTextSecondary">Limitations</h4>
        <ul className="mt-1.5 space-y-1">
          {[
            `Patterns added: ${patternSlugs.size}. The v0 evolution flow is additive — it never prunes, deduplicates, or rewrites existing wiki sections (layer pruning is a later plan item).`,
            `Scorer ${scorer}: the gate compares candidate structure, not model answers — scores are deterministic heuristics; the versioned scorer and eval store are later stories.`,
          ].map((line) => (
            <li key={line} className="text-[11px] text-themeTextMuted leading-relaxed flex gap-1.5">
              <span className="text-themeAccent">›</span>
              <span>{line}</span>
            </li>
          ))}
        </ul>
      </div>

      {/* Rollback: the audited human control over the trained marker */}
      <div className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-4 space-y-2" data-testid="wizard-review-rollback">
        <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
          <Undo2 size={12} className="text-themeAccent" />
          Rollback
        </h4>
        <p className="text-[11px] text-themeTextMuted leading-relaxed">
          Restores the skill body to its pre-training version and records the trained marker as revoked with your reason — never deleted, so the training history stays readable. Audited in the activity log like every other operator control.
        </p>
        <div className="flex flex-wrap gap-2">
          <input
            type="text"
            value={rollbackReason}
            onChange={(e) => setRollbackReason(e.target.value)}
            placeholder="Why is the trained marker being withdrawn?"
            data-testid="wizard-rollback-reason"
            aria-label="Rollback reason"
            className="flex-1 min-w-[14rem] rounded-xl border border-themeBorder bg-themeBgPrimary px-3 py-2 text-xs text-themeTextPrimary placeholder:text-themeTextMuted/70 focus:outline-none focus:border-themeAccent/50"
          />
          <button
            type="button"
            onClick={() => void handleRollback()}
            disabled={!canRollback || rollingBack || !rollbackReason.trim()}
            data-testid="wizard-rollback-button"
            title={canRollback ? 'Restore the pre-training body and revoke the trained marker' : 'No trained marker to roll back'}
            className="inline-flex items-center gap-1.5 py-2 px-3 rounded-xl border border-amber-200 dark:border-amber-900/60 bg-amber-500/10 text-amber-600 dark:text-amber-400 hover:bg-amber-500/20 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
          >
            {rollingBack ? <Loader2 size={12} className="animate-spin" /> : <Undo2 size={12} />}
            <span>{rollingBack ? 'Rolling back…' : 'Roll back'}</span>
          </button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-4">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-xs font-bold text-themeAccent hover:underline cursor-pointer"
        >
          <ArrowRight size={12} className="rotate-180" />
          Back to the live view
        </button>
        <button
          type="button"
          onClick={onRetrain}
          data-testid="wizard-retrain-button"
          className="inline-flex items-center gap-1.5 text-xs font-bold text-themeAccent hover:underline cursor-pointer"
        >
          <RotateCcw size={12} />
          Re-train — restart the flow at the picker
        </button>
      </div>
    </WizardPane>
  );
};

interface WizardStepReportProps {
  jobId: string | null;
  onBack: () => void;
  /**
   * In-app navigation, wired to the app router's navigateTo: pass the article
   * SLUG (not a full path) — navigateTo builds the /articles/{slug} URL.
   */
  onNavigate: (target: string) => void;
}

/**
 * The Report step (story 09): the run's Trained Skill Result report — the one
 * article the wiki generated itself when the run ended. While the run is live
 * the pane says so instead of implying a report exists; a 404 reads as the
 * same honest pending state, not a fault.
 */
export const WizardStepReport: React.FC<WizardStepReportProps> = ({ jobId, onBack, onNavigate }) => {
  const [view, setView] = useState<SkillResultReportView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    if (!jobId) return;
    setLoading(true);
    try {
      const res = await fetch(`/api/evolution/jobs/${encodeURIComponent(jobId)}/report`);
      if (!res.ok) {
        const errBody = await res.json().catch(() => ({}));
        setNotFound(res.status === 404);
        throw new Error(errBody.error || `The report request failed (${res.status})`);
      }
      setView((await res.json()) as SkillResultReportView);
      setError(null);
      setNotFound(false);
    } catch (err: unknown) {
      setView(null);
      setError(err instanceof Error ? err.message : 'Failed to load the report');
    } finally {
      setLoading(false);
    }
  }, [jobId]);

  useEffect(() => {
    // Deferred to the next macrotask like the other views' first poll: the
    // effect body stays free of synchronous setState calls.
    const kick = setTimeout(() => void load(), 0);
    return () => clearTimeout(kick);
  }, [load]);

  if (!jobId) {
    return (
      <WizardPane
        title="6 · Result report"
        subtitle="The Trained Skill Result report is generated once by the wiki itself when a run ends — from the stored records, never a model call."
      >
        <HintCard
          icon={<FileText size={16} />}
          title="No evolution run yet"
          lines={['The report belongs to a run: press Start training run from Step 2 and Start the loop from Step 4, then return here.']}
        />
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-xs font-bold text-themeAccent hover:underline cursor-pointer"
        >
          <ArrowRight size={12} className="rotate-180" />
          Back to the live view
        </button>
      </WizardPane>
    );
  }

  return (
    <WizardPane
      title="6 · Result report"
      subtitle="The Trained Skill Result report: generated once when the run ended, immutable to agents, human-amendable in the editor."
    >
      <div className="flex justify-end">
        <button
          type="button"
          onClick={() => void load()}
          data-testid="wizard-report-refresh"
          title="Refresh the report view"
          aria-label="Refresh the report view"
          className="p-2 rounded-xl border border-themeBorder bg-themeBgPrimary text-themeTextMuted hover:text-themeAccent hover:border-themeAccent/40 transition-colors cursor-pointer"
        >
          <RefreshCw size={13} />
        </button>
      </div>

      {loading && <p className="text-xs text-themeTextMuted">Loading the report…</p>}

      {!loading && error && (
        <HintCard
          icon={<FileText size={16} />}
          title={notFound ? 'No report exists for this run yet' : 'The report could not be loaded'}
          lines={[
            notFound
              ? 'The report publishes when the run ends — the wiki generates it once from the stored records at the stop path. Nothing exists mid-run, and this view refuses to invent one.'
              : error,
            'Use the refresh button once the run has ended.',
          ]}
        />
      )}

      {!loading && !error && view && !view.terminal && (
        <div data-testid="wizard-report-pending" className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-5">
          <h4 className="text-xs font-black text-themeTextSecondary flex items-center gap-1.5">
            <Hourglass size={12} className="text-themeAccent" />
            The report publishes when the run ends
          </h4>
          <p className="mt-2 text-[11px] text-themeTextMuted leading-relaxed">
            Run {view.job_id} is still {view.status}. The wiki generates the report once — when the run reaches a terminal
            state (accepted, exhausted, plateaued, cancelled, or failed) — from the stored records, never a model call.
            Nothing is generated mid-run.
          </p>
          {view.trained_state && <TrainedBadge state={view.trained_state} />}
        </div>
      )}

      {!loading && !error && view && view.terminal && view.report && (
        <div data-testid="wizard-report-view" className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-5 space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <FileText size={14} className="text-themeAccent shrink-0" />
            <h4 className="text-sm font-black text-themeTextPrimary truncate max-w-[24rem]">{view.report.title}</h4>
            {view.loop_outcome && (
              <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${loopOutcomeChipClass(view.loop_outcome)}`}>
                {outcomeLabel(view.loop_outcome)}
              </span>
            )}
          </div>
          <p className="text-[11px] text-themeTextMuted font-mono break-all">run {view.job_id}</p>
          {isRealTimestamp(view.report.created_at) && (
            <p className="text-[11px] text-themeTextMuted">
              Published {new Date(view.report.created_at).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
            </p>
          )}
          {view.trained_state && (
            <div className="flex flex-wrap items-center gap-3" data-testid="wizard-report-trained-state">
              <TrainedBadge state={view.trained_state} />
              {view.trained_state.reason && (
                <p className="text-[11px] text-amber-600 dark:text-amber-400 leading-relaxed min-w-0 flex-1">{view.trained_state.reason}</p>
              )}
            </div>
          )}
          <div className="pt-1">
            <button
              type="button"
              onClick={() => onNavigate(view.report!.slug)}
              data-testid="wizard-report-link"
              title={view.report.url}
              className="inline-flex items-center gap-1.5 py-2 px-3 rounded-xl border border-themeAccent/40 bg-themeAccentBg text-themeAccent hover:border-themeAccent font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer"
            >
              <ExternalLink size={12} />
              <span>Open the report article</span>
            </button>
            <p className="mt-1.5 text-[10px] text-themeTextMuted font-mono break-all">{view.report.url}</p>
          </div>
        </div>
      )}

      {!loading && !error && view && view.terminal && !view.report && (
        <div data-testid="wizard-report-missing" className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 p-5">
          <p className="text-[11px] text-themeTextMuted leading-relaxed">
            Run {view.job_id} ended, but no report article was found for it. Report generation is best-effort at the stop
            path and idempotent — use the refresh button to retry.
          </p>
        </div>
      )}

      <button
        type="button"
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-xs font-bold text-themeAccent hover:underline cursor-pointer"
      >
        <ArrowRight size={12} className="rotate-180" />
        Back to the live view
      </button>
    </WizardPane>
  );
};
