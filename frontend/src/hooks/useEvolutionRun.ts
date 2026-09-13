import { useCallback, useEffect, useRef, useState } from 'react';
import type { EvolutionJob, EvalMeta, IterationRecord, LoopState, SkillCandidate } from '../types';

/**
 * The wizard's live-view data source (story 08): everything one run shows,
 * polled from the story-08 REST seam.
 *
 * POLLING, NOT SSE (a story-08 decision, deliberately recorded): the existing
 * SSE bus (server/event.go) carries article WikiUpdates and activity LogEvents
 * only — the loop stepper writes its per-iteration records without publishing
 * anything, so an SSE live feed would mean extending the bus and every
 * subscriber. Polling the one-request loop endpoint every couple of seconds is
 * enough for an operator's pace; a dedicated evolution event stream remains
 * future work if a real-time feel is ever needed.
 *
 * The hook keeps the last good snapshot while a refresh fails (a flaky request
 * never blanks the view), stops polling once the job goes terminal (a stopped
 * run stays stopped — refresh() restarts the cycle manually), and never issues
 * overlapping requests.
 */

const ACTIVE_POLL_MS = 2000;
/** Exported for tests that advance fake timers past the poll cadence. */
export const EVOLUTION_ACTIVE_POLL_MS = ACTIVE_POLL_MS;

const TERMINAL_STATUSES = ['complete', 'failed', 'timeout', 'cancelled'];

export function isEvolutionJobTerminal(status: string | undefined): boolean {
  return !!status && TERMINAL_STATUSES.includes(status);
}

export interface EvolutionRunData {
  job: EvolutionJob | null;
  loop: LoopState | null;
  iterations: IterationRecord[];
  plateauCount: number;
  maxIterations: number;
  plateauLimit: number;
  evalMeta: EvalMeta | null;
  /** Candidate records fetched for the iteration cards, keyed by candidate id. */
  candidates: Record<string, SkillCandidate>;
  error: string | null;
  /** True until the first successful poll for this job id returns. */
  loading: boolean;
  refresh: () => Promise<void>;
}

export function useEvolutionRun(jobId: string | null): EvolutionRunData {
  const [job, setJob] = useState<EvolutionJob | null>(null);
  const [loop, setLoop] = useState<LoopState | null>(null);
  const [iterations, setIterations] = useState<IterationRecord[]>([]);
  const [plateauCount, setPlateauCount] = useState(0);
  const [maxIterations, setMaxIterations] = useState(0);
  const [plateauLimit, setPlateauLimit] = useState(0);
  const [evalMeta, setEvalMeta] = useState<EvalMeta | null>(null);
  const [candidates, setCandidates] = useState<Record<string, SkillCandidate>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [refreshTick, setRefreshTick] = useState(0);
  const fetching = useRef(false);
  const terminalSeen = useRef(false);
  // Eval metadata is uploaded once per run; this ref lets the poll loop see the
  // latest value without making it a dependency that would restart the interval.
  const fetchedEvalFor = useRef<string | null>(null);
  const knownCandidates = useRef<Set<string>>(new Set());

  const refresh = useCallback(async () => {
    // A manual refresh restarts polling even after a terminal stop.
    terminalSeen.current = false;
    setRefreshTick((t) => t + 1);
  }, []);

  useEffect(() => {
    if (!jobId) {
      // Deferred the same way the first poll is: the reset happens on the next
      // macrotask, keeping the effect body free of synchronous setState calls.
      const clear = setTimeout(() => {
        setJob(null);
        setLoop(null);
        setIterations([]);
        setEvalMeta(null);
        setCandidates({});
        setError(null);
        setLoading(false);
      }, 0);
      return () => clearTimeout(clear);
    }

    let cancelled = false;

    const poll = async () => {
      if (fetching.current) return;
      fetching.current = true;
      try {
        const [jobRes, loopRes] = await Promise.all([
          fetch(`/api/evolution/jobs/${jobId}`),
          fetch(`/api/evolution/jobs/${jobId}/loop`),
        ]);
        if (!jobRes.ok || !loopRes.ok) {
          throw new Error(`Evolution run not found (job ${jobId})`);
        }
        const freshJob: EvolutionJob = await jobRes.json();
        const loopBody = await loopRes.json();
        if (cancelled) return;

        setJob(freshJob);
        setLoop(loopBody.loop ?? null);
        setIterations(Array.isArray(loopBody.iterations) ? loopBody.iterations : []);
        setPlateauCount(loopBody.plateau_count ?? 0);
        setMaxIterations(loopBody.max_iterations ?? 0);
        setPlateauLimit(loopBody.plateau_limit ?? 0);
        setError(null);
        setLoading(false);

        // A missing eval set is the waiting state, not an error — keep the
        // fetch best-effort so the Data/Baseline steps render guidance.
        if (freshJob.eval_hash && fetchedEvalFor.current !== jobId) {
          fetchedEvalFor.current = jobId;
          try {
            const evalRes = await fetch(`/api/evolution/jobs/${jobId}/eval`);
            if (evalRes.ok && !cancelled) {
              const meta: EvalMeta = await evalRes.json();
              setEvalMeta(meta);
            }
          } catch {
            // Refresh will retry; the eval step renders its guidance meanwhile.
            if (!cancelled) fetchedEvalFor.current = null;
          }
        }

        // Candidates the iteration cards need but have not seen yet.
        for (const rec of loopBody.iterations ?? []) {
          const id: string | undefined = rec.candidate_id;
          if (!id || knownCandidates.current.has(id)) continue;
          knownCandidates.current.add(id);
          try {
            const candRes = await fetch(`/api/evolution/candidates/${id}`);
            if (candRes.ok && !cancelled) {
              const cand: SkillCandidate = await candRes.json();
              setCandidates((prev) => ({ ...prev, [id]: cand }));
            }
          } catch {
            // The card renders without the diff; a later refresh retries.
            if (!cancelled) knownCandidates.current.delete(id);
          }
        }

        if (isEvolutionJobTerminal(freshJob.status)) {
          terminalSeen.current = true;
        }
      } catch (err: unknown) {
        if (cancelled) return;
        const msg = err instanceof Error ? err.message : 'Failed to poll the evolution run';
        setError(msg);
        setLoading(false);
      } finally {
        fetching.current = false;
      }
    };

    // First poll on the next macrotask (the interval drives every later one):
    // all setup state happens there, keeping the effect body free of
    // synchronous setState calls, in the shape the app's other views use.
    const kick = setTimeout(() => {
      setLoading(true);
      terminalSeen.current = false;
      fetchedEvalFor.current = null;
      knownCandidates.current = new Set();
      void poll();
    }, 0);
    const timer = setInterval(() => {
      if (terminalSeen.current) return;
      void poll();
    }, ACTIVE_POLL_MS);

    return () => {
      cancelled = true;
      clearTimeout(kick);
      clearInterval(timer);
    };
  }, [jobId, refreshTick]);

  return {
    job,
    loop,
    iterations,
    plateauCount,
    maxIterations,
    plateauLimit,
    evalMeta,
    candidates,
    error,
    loading,
    refresh,
  };
}
