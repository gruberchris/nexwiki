import { HelpCircle, X } from 'lucide-react';

function Row({ code, desc }: { code: string; desc: string }) {
  return (
    <div className="flex items-start gap-3">
      <code className="shrink-0 px-2 py-0.5 rounded-md bg-themeBgSecondary border border-themeBorder text-themeAccent font-mono text-[11px]">
        {code}
      </code>
      <span className="text-themeTextMuted leading-relaxed">{desc}</span>
    </div>
  );
}

interface SidebarFilterHelpModalProps {
  onClose: () => void;
}

export function SidebarFilterHelpModal({ onClose }: SidebarFilterHelpModalProps) {
  return (
    <div
      className="absolute inset-0 z-50 flex items-center justify-center p-4 bg-slate-900/40 dark:bg-black/60 backdrop-blur-[2px] animate-fade-in"
      onClick={onClose}
    >
      <div
        className="relative w-full max-w-[280px] bg-themeBgPrimary border border-themeBorder rounded-2xl shadow-2xl p-5 space-y-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <HelpCircle size={16} className="text-themeAccent" />
            <h2 className="text-sm font-bold text-themeTextPrimary">Sidebar Filter Syntax</h2>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-lg text-themeTextMuted hover:text-themeTextPrimary hover:bg-themeBgSecondary transition-colors"
          >
            <X size={14} />
          </button>
        </div>

        <div className="space-y-4 text-xs text-themeTextSecondary">
          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Basic Search</p>
            <div className="space-y-1.5">
              <Row code="guide" desc="Match articles with 'guide' in title or tags" />
              <Row code="!draft" desc="Exclude articles marked as 'draft'" />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Combining terms</p>
            <div className="space-y-1.5">
              <Row code="api docs" desc="OR — show items matching 'api' or 'docs'" />
              <Row code="api && core" desc="AND — must match both 'api' and 'core'" />
              <Row code="!archived !legacy" desc="Exclude both archived and legacy items" />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Advanced Filtering</p>
            <div className="space-y-1.5">
              <Row code="project-x && !wip" desc="Show Project-X items that aren't 'wip'" />
              <Row code="skill || plan" desc="Show items related to skills or plans" />
            </div>
          </div>
        </div>

        <p className="text-[10px] text-themeTextMuted pt-1 border-t border-themeBorder">
          Filters match against article titles, slugs, and all assigned tags. Operators are case-insensitive.
        </p>
      </div>
    </div>
  );
}
