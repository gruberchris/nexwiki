// OKF document-class discriminator. Every article carries exactly one of these in `type`.
export type ContentType =
  | 'Wiki'
  | 'AI-Agent-Memory'
  | 'AI-Agent-Plan'
  | 'AI-Agent-Skill'
  | 'Attested Computation';

export const ContentTypes = {
  Wiki: 'Wiki',
  Memory: 'AI-Agent-Memory',
  Plan: 'AI-Agent-Plan',
  Skill: 'AI-Agent-Skill',
  Computation: 'Attested Computation',
} as const;

export type TrustTier = 'unverified' | 'machine-confirmed' | 'human-reviewed';

export interface OKFGenerated {
  by: string;
  at?: string;
}

export interface OKFVerification {
  by: string;
  at?: string;
}

export interface OKFUsageWindow {
  from?: string;
  to?: string;
}

export interface OKFSource {
  id?: string;
  resource: string;
  title?: string;
  author?: string;
  usage_count?: number;
  last_modified?: string;
  usage_window?: OKFUsageWindow;
}

export interface OKFParameter {
  name: string;
  type: string;
  required?: boolean;
}

export interface Article {
  type?: ContentType;
  title: string;
  slug: string;
  created_at: string;
  timestamp: string;
  content?: string;
  description?: string;
  source?: string;
  resource?: string;
  version?: number;
  edit_summary?: string;
  tags?: string[];
  archived_at?: string;
  /** Lifecycle state. Plans and skills use a closed vocabulary; other types may use anything. */
  status?: string;
  /** When a plan last changed lifecycle status; drives the auto-archive/auto-delete timers. */
  status_changed_at?: string;
  /**
   * What sort of fact a memory holds: project, reference, user, or feedback. Only ever set on
   * AI-Agent-Memory documents, and absent on memories written before the axis existed. Independent
   * of the `memory-<scope>` tag, which is how far a fact reaches rather than what sort it is.
   */
  memory_kind?: string;
  generated?: OKFGenerated;
  verified?: OKFVerification[];
  trust_tier?: TrustTier;
  sources?: OKFSource[];
  stale_after?: string;
  is_stale?: boolean;
  runtime?: string;
  parameters?: OKFParameter[];
  computation?: string;
}

/** The closed memory-kind vocabulary, mirroring MemoryKinds in server/tags.go. */
export const MemoryKinds = ['project', 'reference', 'user', 'feedback'] as const;

/**
 * A timestamp that represents a real moment, rather than Go's zero value serialized as a string.
 * Declared as a type predicate so callers still narrow away `undefined`, which a plain boolean
 * return would silently stop doing.
 */
export function isRealTimestamp(value: string | undefined): value is string {
  return !!value && !value.startsWith('0001-01-01');
}

/**
 * Whether a document counts as archived — mirroring the server's IsArchived. All three forms are
 * checked because plans and skills archive through the status field while wiki articles and
 * memories archive through the tag, and a caller that inspects only one silently misses half.
 */
export function isArchivedDoc(art: Pick<Article, 'archived_at' | 'tags' | 'status'>): boolean {
  // Not `if (art.archived_at)`. A Go zero time serializes as "0001-01-01T00:00:00Z", which is a
  // truthy string — reading it as a boolean marked every document archived and emptied the
  // dashboard and sidebar while the counts, taken from the unfiltered list, still showed.
  if (isRealTimestamp(art.archived_at)) return true;
  if (art.status?.toLowerCase() === 'archived') return true;
  return !!art.tags?.some((t) => t.toLowerCase() === 'archived');
}

// Classification helpers keyed off the OKF `type` (replacing the old aiagent-* tag scan).
export function isAgentDoc(art: Pick<Article, 'type'>): boolean {
  return !!art.type && art.type !== ContentTypes.Wiki;
}
export function isMemory(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Memory;
}
export function isPlan(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Plan;
}
export function isSkill(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Skill;
}

export function isComputation(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Computation;
}

// Short, human-friendly label for a document type (used in read-only badges).
export function typeLabel(type?: ContentType): string {
  switch (type) {
    case ContentTypes.Memory:
      return 'Agent Memory';
    case ContentTypes.Plan:
      return 'Agent Plan';
    case ContentTypes.Skill:
      return 'Agent Skill';
    case ContentTypes.Computation:
      return 'Attested Computation';
    default:
      return 'Wiki';
  }
}

// Light/dark variant selection mode: explicit choice or follow the browser.
export type ThemeMode = 'light' | 'dark' | 'auto';

// ---------------------------------------------------------------------------
// WikiSkill evolution wizard (story 08). These mirror the Go types in
// server/skill_trained.go, skill_jobs.go, skill_loop.go, skill_eval.go, and
// skill_evolution.go as the REST surface serves them — see
// docs/aiagent_skills.md for the endpoint list.
// ---------------------------------------------------------------------------

/** The closed trained-state vocabulary from server/skill_trained.go. */
export type TrainedStateName = 'untrained' | 'trained' | 'stale';

export interface SkillTrainedState {
  state: TrainedStateName;
  reason?: string;
  trained_at?: string;
  trained_version?: number;
  trained_parent_version?: number;
  trained_candidate?: string;
  trained_val_score?: number;
  trained_eval_hash?: string;
  revoked_at?: string;
  revoked_reason?: string;
  current_version: number;
  current_eval_hash?: string;
}

/** One registry row from GET /api/skills: identity plus derived trained state. */
export interface SkillRegistryEntry {
  name: string;
  title: string;
  description?: string;
  tags?: string[];
  version: number;
  raw_url?: string;
  updated_at?: string;
  trained_state: SkillTrainedState;
}

/** One job record from the evolution endpoints (token_hash withheld on REST). */
export interface EvolutionJob {
  id: string;
  skill_slug: string;
  candidate_id?: string;
  profile: string;
  status: 'queued' | 'claimed' | 'running' | 'paused' | 'complete' | 'failed' | 'timeout' | 'cancelled';
  iteration: number;
  max_iterations: number;
  runner?: string;
  pid?: number;
  progress?: number;
  progress_note?: string;
  eval_hash?: string;
  eval_train_count?: number;
  eval_val_count?: number;
  baseline_s0?: number;
  baseline_scorer?: string;
  eval_uploaded_at?: string;
  loop_outcome?: string;
  approve_requested?: boolean;
  plateau_limit?: number;
  checkpoint?: string;
  pause_requested?: boolean;
  cancel_reason?: string;
  error?: string;
  created_at: string;
  updated_at: string;
  claimed_at?: string;
  last_heartbeat?: string;
  lease_expires_at?: string;
  run_deadline_at: string;
  completed_at?: string;
}

export interface LoopPhaseEntry {
  phase: string;
  entered_at: string;
  exited_at?: string;
  note?: string;
  complete: boolean;
}

/** One iteration of the evolution loop, from server/skill_loop.go. */
export interface IterationRecord {
  job_id: string;
  skill_slug: string;
  iteration: number;
  started_at: string;
  completed_at?: string;
  phases: LoopPhaseEntry[];
  candidate_id?: string;
  pattern_slugs?: string[];
  val_score: number;
  r_best: number;
  /** accepted | rejected | interrupted | "" (in flight). */
  outcome: string;
  result_version?: number;
  note?: string;
}

/** The loop stepper's current position, from server/skill_loop.go. */
export interface LoopState {
  job_id: string;
  skill_slug: string;
  status: 'running' | 'paused' | 'terminal';
  current_iteration: number;
  current_phase: string;
  phase_status?: string;
  note?: string;
  outcome?: string;
  outcome_reason?: string;
  started_at: string;
  updated_at: string;
}

/** GET /api/evolution/jobs/{id}/loop — one poll's body for the live view. */
export interface EvolutionLoopResponse {
  loop: LoopState | null;
  iterations: IterationRecord[];
  plateau_count: number;
  max_iterations: number;
  plateau_limit: number;
}

export interface SkillCandidate {
  id: string;
  parent_slug: string;
  parent_version: number;
  diff?: string;
  proposed_body?: string;
  pattern_slugs?: string[];
  proposer: string;
  created_at: string;
  status: 'pending' | 'promoted' | 'rejected';
  result_version?: number;
  best_score?: number;
  decided_at?: string;
}

export interface EvalDryRunSample {
  index: number;
  input_preview: string;
  expected_preview: string;
  pass: boolean;
}

export interface EvalCostEstimate {
  estimated_val_cases: number;
  estimated_cost_usd: number;
  budget_enforced: boolean;
  note: string;
}

/** Stored eval metadata from GET /api/evolution/jobs/{id}/eval. */
export interface EvalMeta {
  job_id: string;
  skill_slug: string;
  filename: string;
  split_mode: string;
  train_count: number;
  val_count: number;
  eval_hash: string;
  baseline_s0: number;
  scorer_version: string;
  dry_run: EvalDryRunSample[];
  estimate: EvalCostEstimate;
  uploaded_at: string;
}

// ---------------------------------------------------------------------------
// Story 09: the Review + Report steps. These mirror the Go types in
// server/skill_audit.go, skill_trained.go (SkillTrainedStateEntry), and
// skill_report.go as the REST surface serves them.
// ---------------------------------------------------------------------------

/** One gate decision from GET /api/skills/{slug}/audit (server/skill_audit.go). */
export interface SkillAuditRecord {
  candidate_id: string;
  skill_slug: string;
  parent_version: number;
  content_hash: string;
  validation_score: number;
  r_best_before: number;
  r_best_after: number;
  /** accepted | rejected | revoked (a rollback's or unlock's revocation). */
  outcome: string;
  /** gate | human */
  decider: string;
  scorer_version: string;
  reason?: string;
  timestamp: string;
}

/** GET /api/skills/{slug}/audit — the skill's trail plus its derived running best. */
export interface SkillAuditTrailView {
  slug: string;
  r_best: number;
  records: SkillAuditRecord[];
}

/**
 * GET /api/skills/{slug}/trained-state, and the POST /api/skills/{slug}/rollback
 * response body: the skill's identity plus its embedded derived trained state
 * (Go embeds the struct, so the JSON is flattened).
 */
export interface SkillTrainedStateEntry extends SkillTrainedState {
  slug: string;
  title: string;
}

/** The report-article pointer inside the Report-step view (server/skill_report.go). */
export interface SkillResultReportRef {
  slug: string;
  title: string;
  /** The article's canonical /articles/{slug} URL. */
  url: string;
  created_at?: string;
}

/** GET /api/evolution/jobs/{id}/report — the Report-step view (story 09). */
export interface SkillResultReportView {
  job_id: string;
  skill_slug: string;
  status: string;
  loop_outcome?: string;
  terminal: boolean;
  /** The Trained Skill Result article — absent until the run ends and it exists. */
  report?: SkillResultReportRef;
  /** The skill's live derived trained state (staleness is derived at serve time). */
  trained_state?: SkillTrainedState;
}
