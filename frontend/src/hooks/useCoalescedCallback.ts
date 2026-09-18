import { useCallback, useEffect, useRef } from 'react';

// useCoalescedCallback returns a stable function that collapses a burst of calls into at most two
// runs of fn: one at once for the first call, and one trailing run delayMs after the calls stop, if
// any more arrived in between.
//
// It exists for live wiki updates. The server announces a bulk write (an OKF import, a global tag
// deletion) with one update per document, and reloading the article list for each one turned a
// single import into hundreds of identical requests. Running the first call immediately keeps an
// isolated update as prompt as before, and the trailing run picks up whatever the burst changed
// after that first reload started.
export function useCoalescedCallback(fn: () => void, delayMs: number): () => void {
  // The latest fn, so a trailing run never calls a stale closure from an earlier render.
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingRef = useRef(false);

  // A trailing run must not fire after the component that asked for it is gone.
  useEffect(() => () => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  return useCallback(() => {
    if (timerRef.current === null) {
      pendingRef.current = false;
      fnRef.current();
    } else {
      pendingRef.current = true;
      clearTimeout(timerRef.current);
    }
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      if (pendingRef.current) {
        pendingRef.current = false;
        fnRef.current();
      }
    }, delayMs);
  }, [delayMs]);
}
