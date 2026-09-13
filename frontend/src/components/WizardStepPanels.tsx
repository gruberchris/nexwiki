import React from 'react';
import type { EvalMeta, EvolutionJob, SkillRegistryEntry } from '../types';
import { Database, FlaskConical, Gauge, UploadCloud, ArrowRight, CheckCircle2, XCircle, Sparkles, ClipboardList, FileText } from 'lucide-react';
import { TrainedBadge } from './TrainedBadge';

/**
 * The wizard's per-step detail panes (story 08). Every pane renders server
 * state — the picker rows come from the registry's derived trained state, the
 * data and baseline panes read the job record and the stored eval metadata,
 * and the review/report panes are honest placeholders for story 09 (which
 * adds the review tables and the auto-generated Result article). Nothing here
 * fakes progress: a step whose server state has not arrived yet says so.
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
}

export const WizardStepData: React.FC<WizardStepDataProps> = ({ job, evalMeta }) => {
  if (!job) {
    return (
      <WizardPane title="2 · Training data" subtitle="The run's validated {input, expected} cases, stored under the job after the format gate accepts them.">
        <HintCard
          icon={<Database size={16} />}
          title="No evolution run yet"
          lines={[
            'The wizard follows a run that already exists — dispatch one from your harness with create_evolution_job, then return here.',
            'Once the run exists, upload the eval set through upload_evolution_eval_set (the harness or agent holds the job token).',
          ]}
        />
      </WizardPane>
    );
  }
  if (!evalMeta) {
    return (
      <WizardPane title="2 · Training data" subtitle="Waiting for the format gate to accept the run's training data.">
        <HintCard
          icon={<UploadCloud size={16} />}
          title="No eval set uploaded yet"
          lines={[
            `Job ${job.id} has no accepted training-data upload yet.`,
            'Upload it with upload_evolution_eval_set (CSV, JSONL, JSON, text, or logs carrying {input, expected} cases) — the browser never holds the job token, so the harness or an agent call does this.',
            'The gate stores train/val splits, the eval hash, and refuses anything with secrets, dupes, or leakage — nothing is stored on a refusal.',
          ]}
        />
      </WizardPane>
    );
  }
  return (
    <WizardPane title="2 · Training data" subtitle="What the format gate accepted and stored for this run.">
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
        <p className="text-[10px] text-themeTextMuted leading-relaxed">
          The eval hash is what the trained marker will be checked against later: re-uploading after
          training is exactly what marks the skill stale.
        </p>
      </div>
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
    <WizardPane title="3 · Baseline" subtitle="S0 — the live skill's score on the validation split under the v0 scorer. Every accepted iteration must beat R_best, which starts here.">
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

// --- Steps 5 & 6: story-09 placeholders ---------------------------------------------

export const WizardStepReviewStub: React.FC<{ onBack: () => void }> = ({ onBack }) => (
  <WizardPane
    title="5 · Review"
    subtitle="The review surface is story 09 of the WikiSkill evolution plan — this wizard stops at the live view and hands off honestly."
  >
    <HintCard
      icon={<ClipboardList size={16} />}
      title="Coming in story 09 (review + report handoff)"
      lines={[
        'Review tables over the full iteration history — every candidate, its diff, and its gate decision with the audit numbers.',
        'Rollback and re-train controls: restore the pre-training body or unlock the marker, each recorded in the skill audit trail.',
        'Eval-hash lineage: which training-data version produced which trained marker.',
      ]}
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

export const WizardStepReportStub: React.FC<{ onBack: () => void }> = ({ onBack }) => (
  <WizardPane
    title="6 · Result report"
    subtitle="The Trained Skill Result report is story 09 — nothing is generated here, so there is nothing fake to read."
  >
    <HintCard
      icon={<FileText size={16} />}
      title="Coming in story 09 (auto-generated Result article)"
      lines={[
        'On acceptance AND on fail/exhaust/cancel, story 09 writes a harness-authored Result report article (human-amendable only).',
        'It links the skill ↔ report pair and carries the gate history, eval hashes, and scorer version.',
        'No report exists for this run yet — this step refuses to invent one.',
      ]}
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
