import { useCallback, useEffect, useState } from 'react';

const LS_DRAFT = 'dawui.draft.';

interface DraftEntry {
  sessionId: string | null;
  text: string;
}

function loadDraft(sessionId: string | null): DraftEntry {
  if (!sessionId) return { sessionId, text: '' };
  try {
    return { sessionId, text: localStorage.getItem(LS_DRAFT + sessionId) ?? '' };
  } catch {
    return { sessionId, text: '' };
  }
}

/** Persists a separate composer draft for each stable session id. */
export function useDraft(sessionId: string | null) {
  const [entry, setEntry] = useState<DraftEntry>(() => loadDraft(sessionId));

  useEffect(() => {
    setEntry(loadDraft(sessionId));
  }, [sessionId]);

  const setDraft = useCallback(
    (text: string) => {
      setEntry({ sessionId, text });
      if (!sessionId) return;
      try {
        if (text) localStorage.setItem(LS_DRAFT + sessionId, text);
        else localStorage.removeItem(LS_DRAFT + sessionId);
      } catch {
        /* Storage is optional. */
      }
    },
    [sessionId],
  );

  return { draft: entry.sessionId === sessionId ? entry.text : '', setDraft };
}
