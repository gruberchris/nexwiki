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

interface ActivityFilterHelpModalProps {
  onClose: () => void;
}

export function ActivityFilterHelpModal({ onClose }: ActivityFilterHelpModalProps) {
  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm animate-fade-in"
      onClick={onClose}
    >
      <div
        className="relative w-full max-w-md bg-themeBgPrimary border border-themeBorder rounded-2xl shadow-2xl p-6 space-y-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <HelpCircle size={16} className="text-themeAccent" />
            <h2 className="text-sm font-bold text-themeTextPrimary">Activity Filter Syntax</h2>
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
            <p className="font-semibold text-themeTextPrimary">Filter by Event Data</p>
            <div className="space-y-1.5">
              <Row code="mcp" desc="Match events from the MCP Server source" />
              <Row code="edit" desc='Match events with the "edit" action' />
              <Row code="read_article" desc="Match events using a specific tool" />
              <Row code="!api" desc="Exclude all REST API activity" />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Combining terms</p>
            <div className="space-y-1.5">
              <Row code="edit create" desc="OR — show edits or creations (space = OR)" />
              <Row code="mcp && edit" desc="AND — MCP source events that are edits" />
              <Row code="!api !read" desc="Exclude both API events and any read actions" />
            </div>
          </div>

          <div className="space-y-2">
            <p className="font-semibold text-themeTextPrimary">Operator & Tool matching</p>
            <div className="space-y-1.5">
              <Row code="AI && create" desc='Show creations made by the "AI Agent"' />
              <Row code="User !delete" desc="User activity excluding deletions" />
              <Row code="mcp !list_articles" desc="MCP activity excluding list operations" />
            </div>
          </div>
        </div>

        <p className="text-[10px] text-themeTextMuted pt-1 border-t border-themeBorder">
          Filters match against actions, sources, tools, slugs, titles, and agents. Operators are case-insensitive.
        </p>
      </div>
    </div>
  );
}
