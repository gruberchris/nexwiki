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

interface FilterHelpModalProps {
  onClose: () => void;
}

export function FilterHelpModal({ onClose }: FilterHelpModalProps) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm animate-fade-in"
      onClick={onClose}
    >
      <div
        className="relative w-full max-w-md bg-themeBgPrimary border border-themeBorder rounded-2xl shadow-2xl p-6 space-y-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <HelpCircle size={16} className="text-themeAccent" />
            <h2 className="text-sm font-bold text-themeTextPrimary">Filter Syntax</h2>
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
            <p className="font-semibold text-themeTextPrimary">Basic</p>
            <div className="space-y-1.5">
              <Row code="nexwiki" desc='Match title or any tag containing "nexwiki"' />
              <Row code="!completed" desc='Exclude items where title or any tag contains "completed"' />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Combining terms</p>
            <div className="space-y-1.5">
              <Row code="nex wiki" desc="OR — show items matching either term (space = OR)" />
              <Row code="nex OR wiki" desc="OR — explicit, same as space" />
              <Row code="nex || wiki" desc="OR — symbol alias" />
              <Row code="nex AND wiki" desc="AND — both terms must match" />
              <Row code="nex && wiki" desc="AND — symbol alias" />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Negations</p>
            <div className="space-y-1.5">
              <Row code="!completed !wip" desc="Exclude both — negations are always ANDed" />
              <Row code="!completed OR !wip" desc="Same result — OR between negations still excludes both" />
              <Row code="nex !completed" desc='Matches "nex" and excludes "completed"' />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Complex</p>
            <div className="space-y-1.5">
              <Row code="nex AND wiki || nexwiki" desc="(nex AND wiki) OR nexwiki — AND binds tighter" />
              <Row code="draft OR wip !archived" desc="(draft OR wip) and not archived" />
            </div>
          </div>
        </div>

        <p className="text-[10px] text-themeTextMuted pt-1 border-t border-themeBorder">
          Filters match against article titles and all assigned tags. Operators are case-insensitive.
        </p>
      </div>
    </div>
  );
}
