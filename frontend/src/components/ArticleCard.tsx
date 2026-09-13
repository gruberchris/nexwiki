import type { Article, SkillTrainedState } from '../types';
import { isAgentDoc, isSkill, typeLabel } from '../types';
import { formatRelativeTime } from '../utils';
import { Clock, ArrowRight, Wrench } from 'lucide-react';
import { sortCardTags } from '../filterUtils';
import { statusBadgeClass } from '../statusTags';
import { memoryKindBadgeClass } from '../memoryKinds';
import { TrainedBadge } from './TrainedBadge';

const MAX_VISIBLE_TAGS = 3;

interface ArticleCardProps {
  art: Article;
  onNavigate: (slug: string) => void;
  secondary?: boolean;
  statusTags: Set<string>;
  /**
   * The skill's derived trained state from the registry (story 08): rendered as
   * the trained badge on skill cards only, with the "Train with WikiSkill"
   * entry point next to it.
   */
  trainedState?: SkillTrainedState | null;
  onTrain?: (slug: string) => void;
}

export function ArticleCard({ art, onNavigate, secondary = false, statusTags, trainedState, onTrain }: ArticleCardProps) {
  const accentText = secondary ? 'text-themeAccentSecondary' : 'text-themeAccent';
  const accentHover = secondary ? 'group-hover:text-themeAccentSecondary' : 'group-hover:text-themeAccent';

  return (
    <div
      onClick={() => onNavigate(art.slug)}
      className="p-5 rounded-2xl border border-themeBorder bg-themeBgSecondary/40 hover:bg-themeBgSecondary hover:border-themeBorder shadow-xs hover:shadow-md cursor-pointer group flex flex-col justify-between min-h-[120px] transition-all duration-200"
    >
      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <h3 className={`text-sm font-bold text-themeTextSecondary ${accentHover} truncate transition-colors`}>
            {art.title}
          </h3>
          {isSkill(art) && <TrainedBadge state={trainedState ?? null} />}
        </div>
        {art.description && (
          <p className="text-[11px] text-themeTextMuted line-clamp-2 leading-snug">
            {art.description}
          </p>
        )}
        {(isAgentDoc(art) || art.status || art.memory_kind || (art.tags && art.tags.length > 0)) && (
          <div className="flex flex-wrap gap-1">
            {art.status && (
              <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-semibold ${statusBadgeClass(art.status)}`}>
                {art.status}
              </span>
            )}
            {art.memory_kind && (
              <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-semibold ${memoryKindBadgeClass(art.memory_kind)}`}>
                {art.memory_kind}
              </span>
            )}
            {isAgentDoc(art) && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-themeBgSecondary text-themeTextMuted border border-themeBorder font-semibold">
                {typeLabel(art.type)}
              </span>
            )}
            {art.tags && sortCardTags(art.tags, statusTags).slice(0, MAX_VISIBLE_TAGS).map((tag) => (
              <span
                key={tag}
                className="text-[10px] px-1.5 py-0.5 rounded-full bg-themeAccentBg text-themeAccent font-medium"
              >
                {tag}
              </span>
            ))}
            {art.tags && art.tags.length > MAX_VISIBLE_TAGS && (
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
          <span>Updated {formatRelativeTime(art.timestamp)}</span>
        </div>
        {isSkill(art) && onTrain ? (
          <button
            type="button"
            data-testid={`train-skill-button-${art.slug}`}
            onClick={(e) => {
              // The card itself navigates to the skill; the train button must
              // not bubble that click through.
              e.stopPropagation();
              onTrain(art.slug);
            }}
            title="Train with WikiSkill — open the evolution wizard"
            className={`flex items-center gap-0.5 ${accentText} font-semibold hover:underline cursor-pointer`}
          >
            <Wrench size={10} />
            Train
          </button>
        ) : (
          <span className={`flex items-center gap-0.5 ${accentText} font-semibold group-hover:translate-x-1 transition-transform`}>
            Open <ArrowRight size={10} />
          </span>
        )}
      </div>
    </div>
  );
}
