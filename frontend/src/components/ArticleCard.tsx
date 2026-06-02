import type { Article } from '../types';
import { formatRelativeTime } from '../utils';
import { Clock, ArrowRight } from 'lucide-react';
import { sortCardTags } from '../filterUtils';

const MAX_VISIBLE_TAGS = 3;

interface ArticleCardProps {
  art: Article;
  onNavigate: (slug: string) => void;
  secondary?: boolean;
  statusTags: Set<string>;
}

export function ArticleCard({ art, onNavigate, secondary = false, statusTags }: ArticleCardProps) {
  const accentText = secondary ? 'text-themeAccentSecondary' : 'text-themeAccent';
  const accentHover = secondary ? 'group-hover:text-themeAccentSecondary' : 'group-hover:text-themeAccent';

  return (
    <div
      onClick={() => onNavigate(art.slug)}
      className="p-5 rounded-2xl border border-themeBorder bg-themeBgSecondary/40 hover:bg-themeBgSecondary hover:border-themeBorder shadow-sm hover:shadow-md cursor-pointer group flex flex-col justify-between min-h-[120px] transition-all duration-200"
    >
      <div className="space-y-1.5">
        <h3 className={`text-sm font-bold text-themeTextSecondary ${accentHover} truncate transition-colors`}>
          {art.title}
        </h3>
        {art.tags && art.tags.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {sortCardTags(art.tags, statusTags).slice(0, MAX_VISIBLE_TAGS).map((tag) => {
              const isSystem = tag.toLowerCase().startsWith('aiagent-');
              return (
                <span
                  key={tag}
                  className={isSystem
                    ? 'text-[10px] px-1.5 py-0.5 rounded-full bg-themeBgSecondary text-themeTextMuted border border-themeBorder'
                    : 'text-[10px] px-1.5 py-0.5 rounded-full bg-themeAccentBg text-themeAccent font-medium'
                  }
                >
                  {tag}
                </span>
              );
            })}
            {art.tags.length > MAX_VISIBLE_TAGS && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-themeBgSecondary text-themeTextMuted border border-themeBorder">
                +{art.tags.length - MAX_VISIBLE_TAGS} more
              </span>
            )}
          </div>
        )}
      </div>

      <div className="flex items-center justify-between border-t border-themeBorder pt-3 mt-4 text-[10px] text-themeTextMuted select-none">
        <div className="flex items-center gap-1">
          <Clock size={11} />
          <span>Updated {formatRelativeTime(art.updated_at)}</span>
        </div>
        <span className={`flex items-center gap-0.5 ${accentText} font-semibold group-hover:translate-x-1 transition-transform`}>
          Open <ArrowRight size={10} />
        </span>
      </div>
    </div>
  );
}
