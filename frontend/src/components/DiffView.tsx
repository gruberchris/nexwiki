import React from 'react';
import { Columns, List } from 'lucide-react';

interface DiffLine {
  type: 'added' | 'removed' | 'unchanged';
  value: string;
  oldLineNumber?: number;
  newLineNumber?: number;
}

interface DiffViewProps {
  oldContent: string;
  newContent: string;
  oldTitle: string;
  newTitle: string;
  layoutMode: 'split' | 'unified';
  onLayoutChange: (mode: 'split' | 'unified') => void;
}

export const DiffView: React.FC<DiffViewProps> = ({
  oldContent,
  newContent,
  oldTitle,
  newTitle,
  layoutMode,
  onLayoutChange,
}) => {
  // Simple, highly robust line-by-line diff algorithm
  const getDiffLines = (): DiffLine[] => {
    const oldLines = oldContent.split('\n');
    const newLines = newContent.split('\n');
    const result: DiffLine[] = [];

    let i = 0, j = 0;
    while (i < oldLines.length || j < newLines.length) {
      if (i < oldLines.length && j < newLines.length && oldLines[i] === newLines[j]) {
        result.push({
          type: 'unchanged',
          value: oldLines[i],
          oldLineNumber: i + 1,
          newLineNumber: j + 1,
        });
        i++;
        j++;
      } else if (j < newLines.length && (i >= oldLines.length || !oldLines.slice(i).includes(newLines[j]))) {
        result.push({
          type: 'added',
          value: newLines[j],
          newLineNumber: j + 1,
        });
        j++;
      } else {
        result.push({
          type: 'removed',
          value: oldLines[i],
          oldLineNumber: i + 1,
        });
        i++;
      }
    }
    return result;
  };

  const diffLines = getDiffLines();

  return (
    <div className="flex-1 flex flex-col h-full bg-white dark:bg-slate-900 rounded-2xl border border-slate-200/80 dark:border-slate-800/80 shadow-lg overflow-hidden animate-fade-in">
      
      {/* Diff View Header Controls */}
      <div className="p-4 border-b border-slate-200/80 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/50 backdrop-blur-md flex items-center justify-between gap-4 select-none">
        <div className="space-y-1">
          <h3 className="text-xs font-bold text-slate-800 dark:text-slate-100 uppercase tracking-wider">Comparison Diffs</h3>
          <p className="text-[10px] text-slate-400 font-mono">
            Comparing: {oldTitle} ➔ {newTitle}
          </p>
        </div>

        {/* Layout Selector Controls */}
        <div className="flex items-center gap-1 rounded-xl bg-slate-200/60 dark:bg-slate-950 p-1">
          <button
            type="button"
            onClick={() => onLayoutChange('split')}
            className={`py-1.5 px-3 rounded-lg flex items-center gap-1.5 text-[10px] font-bold transition-all ${
              layoutMode === 'split'
                ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm scale-102'
                : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
            }`}
          >
            <Columns size={12} />
            <span>Split Pane</span>
          </button>
          <button
            type="button"
            onClick={() => onLayoutChange('unified')}
            className={`py-1.5 px-3 rounded-lg flex items-center gap-1.5 text-[10px] font-bold transition-all ${
              layoutMode === 'unified'
                ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm scale-102'
                : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
            }`}
          >
            <List size={12} />
            <span>Unified Inline</span>
          </button>
        </div>
      </div>

      {/* Render Engine Content */}
      <div className="flex-1 overflow-auto bg-slate-50/20 dark:bg-slate-950/10 p-4 min-h-[300px]">
        {layoutMode === 'unified' ? (
          /* ================= UNIFIED INLINE LAYOUT ================= */
          <div className="font-mono text-xs leading-relaxed border border-slate-200/60 dark:border-slate-800/60 rounded-xl overflow-hidden divide-y divide-slate-100 dark:divide-slate-900 bg-white dark:bg-slate-900 shadow-sm">
            {diffLines.map((line, index) => {
              const isAdded = line.type === 'added';
              const isRemoved = line.type === 'removed';
              return (
                <div
                  key={index}
                  className={`grid grid-cols-[45px_45px_1fr] group ${
                    isAdded
                      ? 'bg-emerald-500/10 text-emerald-800 dark:text-emerald-300 border-l-4 border-emerald-500'
                      : isRemoved
                      ? 'bg-rose-500/10 text-rose-800 dark:text-rose-300 border-l-4 border-rose-500'
                      : 'text-slate-600 dark:text-slate-400 border-l-4 border-transparent hover:bg-slate-50 dark:hover:bg-slate-800/30'
                  }`}
                >
                  {/* Line Numbers */}
                  <span className="text-[9px] font-semibold text-slate-450 dark:text-slate-500 select-none text-right pr-3 py-1 border-r border-slate-150 dark:border-slate-800/60">
                    {line.oldLineNumber || ''}
                  </span>
                  <span className="text-[9px] font-semibold text-slate-450 dark:text-slate-500 select-none text-right pr-3 py-1 border-r border-slate-150 dark:border-slate-800/60">
                    {line.newLineNumber || ''}
                  </span>
                  {/* Content line */}
                  <span className="pl-4 py-1 whitespace-pre-wrap select-text break-all">
                    <span className="font-bold select-none mr-2">
                      {isAdded ? '+' : isRemoved ? '-' : ' '}
                    </span>
                    {line.value}
                  </span>
                </div>
              );
            })}
          </div>
        ) : (
          /* ================= SPLIT PANE LAYOUT ================= */
          <div className="grid grid-cols-2 divide-x divide-slate-200 dark:divide-slate-800 border border-slate-200/60 dark:border-slate-800/60 rounded-xl overflow-hidden bg-white dark:bg-slate-900 shadow-sm">
            
            {/* Left Pane (Old / Deletions) */}
            <div className="flex flex-col font-mono text-xs leading-relaxed overflow-x-auto divide-y divide-slate-100 dark:divide-slate-900">
              <div className="p-3 bg-slate-50/50 dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 text-[10px] uppercase font-bold tracking-wider text-slate-400">
                {oldTitle}
              </div>
              {diffLines
                .filter((l) => l.type !== 'added')
                .map((line, idx) => (
                  <div
                    key={idx}
                    className={`grid grid-cols-[40px_1fr] py-1 ${
                      line.type === 'removed'
                        ? 'bg-rose-500/10 text-rose-800 dark:text-rose-300 font-semibold border-l-4 border-rose-500'
                        : 'text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/30 border-l-4 border-transparent'
                    }`}
                  >
                    <span className="text-[9px] text-right text-slate-400 select-none pr-3 border-r border-slate-150 dark:border-slate-800/60">
                      {line.oldLineNumber}
                    </span>
                    <span className="pl-4 whitespace-pre-wrap select-text break-all">
                      <span className="font-bold select-none mr-2">
                        {line.type === 'removed' ? '-' : ' '}
                      </span>
                      {line.value}
                    </span>
                  </div>
                ))}
            </div>

            {/* Right Pane (New / Additions) */}
            <div className="flex flex-col font-mono text-xs leading-relaxed overflow-x-auto divide-y divide-slate-100 dark:divide-slate-900">
              <div className="p-3 bg-slate-50/50 dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 text-[10px] uppercase font-bold tracking-wider text-slate-400">
                {newTitle}
              </div>
              {diffLines
                .filter((l) => l.type !== 'removed')
                .map((line, idx) => (
                  <div
                    key={idx}
                    className={`grid grid-cols-[40px_1fr] py-1 ${
                      line.type === 'added'
                        ? 'bg-emerald-500/10 text-emerald-800 dark:text-emerald-300 font-semibold border-l-4 border-emerald-500'
                        : 'text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/30 border-l-4 border-transparent'
                    }`}
                  >
                    <span className="text-[9px] text-right text-slate-400 select-none pr-3 border-r border-slate-150 dark:border-slate-800/60">
                      {line.newLineNumber}
                    </span>
                    <span className="pl-4 whitespace-pre-wrap select-text break-all">
                      <span className="font-bold select-none mr-2">
                        {line.type === 'added' ? '+' : ' '}
                      </span>
                      {line.value}
                    </span>
                  </div>
                ))}
            </div>

          </div>
        )}
      </div>
    </div>
  );
};
