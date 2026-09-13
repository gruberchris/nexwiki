import React from 'react';

/**
 * A read-only renderer for the unified diff a candidate carries (story 08).
 * The candidate record is inert data — the wizard only colors and indents the
 * lines; it never applies anything. Colors ride the same added/removed palette
 * the app's DiffView uses, so the reading is consistent across surfaces.
 */

interface WizardDiffViewProps {
  diff: string;
}

interface DiffRow {
  kind: 'add' | 'remove' | 'hunk' | 'meta' | 'context';
  text: string;
}

function classify(line: string): DiffRow {
  if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff ') || line.startsWith('index ')) {
    return { kind: 'meta', text: line };
  }
  if (line.startsWith('@@')) return { kind: 'hunk', text: line };
  if (line.startsWith('+')) return { kind: 'add', text: line };
  if (line.startsWith('-')) return { kind: 'remove', text: line };
  return { kind: 'context', text: line };
}

const ROW_CLASSES: Record<DiffRow['kind'], string> = {
  add: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  remove: 'bg-rose-500/10 text-rose-700 dark:text-rose-300',
  hunk: 'bg-themeAccentBg text-themeAccent',
  meta: 'text-themeTextMuted',
  context: 'text-themeTextSecondary',
};

export const WizardDiffView: React.FC<WizardDiffViewProps> = ({ diff }) => {
  const rows = diff.split('\n').map(classify);
  return (
    <div
      data-testid="wizard-diff-view"
      className="rounded-xl border border-themeBorder overflow-hidden bg-themeBgPrimary"
    >
      <div className="px-3 py-1.5 text-[10px] font-bold uppercase tracking-wider text-themeTextMuted border-b border-themeBorder bg-themeBgSecondary/60">
        Candidate diff (read-only)
      </div>
      <pre className="p-3 overflow-x-auto text-[11px] leading-relaxed font-mono max-h-72 overflow-y-auto">
        {rows.map((row, i) => (
          <div key={i} className={ROW_CLASSES[row.kind]}>
            {row.text || '\u00A0'}
          </div>
        ))}
      </pre>
    </div>
  );
};
