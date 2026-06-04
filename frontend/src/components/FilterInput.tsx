import React, { useState, useRef } from 'react';
import { Search, X, HelpCircle } from 'lucide-react';
import { applyAutocompleteSelection } from '../filterUtils';
import { useClickOutside } from '../hooks/useClickOutside';

interface FilterInputProps {
  value: string;
  onChange: (value: string) => void;
  suggestions: Array<{ type: string; value: string }>;
  placeholder?: string;
  onOpenHelp?: () => void;
  /** Extra classes applied to the <input> element */
  inputClassName?: string;
  /** Extra classes applied to the outer flex wrapper */
  className?: string;
  /** Tailwind focus-ring class (default: focus:ring-themeAccent) */
  accentRingClass?: string;
  /** Tailwind hover class for the help button (default: hover:text-themeAccent) */
  accentHoverClass?: string;
}

export const FilterInput: React.FC<FilterInputProps> = ({
  value,
  onChange,
  suggestions,
  placeholder = 'Filter...',
  onOpenHelp,
  inputClassName = '',
  className = '',
  accentRingClass = 'focus:ring-themeAccent',
  accentHoverClass = 'hover:text-themeAccent',
}) => {
  const [showDropdown, setShowDropdown] = useState(false);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const [prevSuggestionsLength, setPrevSuggestionsLength] = useState(suggestions.length);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useClickOutside(dropdownRef, () => setShowDropdown(false));

  // React "adjust state while rendering" pattern: reset focusedIndex when the
  // suggestion list changes size. This avoids a useEffect (which would cascade)
  // and is explicitly endorsed by the React docs for deriving state from props.
  if (suggestions.length !== prevSuggestionsLength) {
    setFocusedIndex(-1);
    setPrevSuggestionsLength(suggestions.length);
  }

  const handleSelect = (suggestion: { type: string; value: string }) => {
    onChange(applyAutocompleteSelection(value, suggestion.value));
    setShowDropdown(false);
    setFocusedIndex(-1);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (suggestions.length === 0) return;

    if (e.key === 'Tab') {
      e.preventDefault();
      setFocusedIndex(prev =>
        e.shiftKey
          ? prev <= -1 ? suggestions.length - 1 : prev - 1
          : prev >= suggestions.length - 1 ? -1 : prev + 1
      );
      return;
    }

    if (e.key === 'Enter' && focusedIndex >= 0 && focusedIndex < suggestions.length) {
      e.preventDefault();
      handleSelect(suggestions[focusedIndex]);
      return;
    }

    if (e.key === 'Escape') {
      setShowDropdown(false);
      setFocusedIndex(-1);
    }
  };

  return (
    <div className={`flex items-center gap-1.5 animate-fade-in ${className}`}>
      <div ref={dropdownRef} className="relative flex-1 group z-30">
        <Search
          size={14}
          className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted group-focus-within:text-themeAccent transition-colors"
        />
        <input
          type="text"
          placeholder={placeholder}
          value={value}
          onFocus={() => setShowDropdown(true)}
          onChange={e => {
            onChange(e.target.value);
            setShowDropdown(true);
          }}
          onKeyDown={handleKeyDown}
          className={`w-full pl-10 pr-9 py-2 text-xs rounded-xl border border-themeBorder focus:outline-none focus:ring-2 ${accentRingClass} text-themeTextSecondary transition-all placeholder:text-themeTextMuted ${inputClassName}`}
        />
        {value && (
          <button
            onClick={() => onChange('')}
            className="absolute inset-y-0 right-3 flex items-center text-themeTextMuted hover:text-rose-500 transition-colors animate-fade-in"
          >
            <X size={12} />
          </button>
        )}

        {showDropdown && suggestions.length > 0 && (
          <div className="absolute left-0 top-full mt-1.5 z-50 w-full bg-themeBgSecondary backdrop-blur-lg border border-themeBorder shadow-xl rounded-2xl max-h-48 overflow-y-auto py-1.5 select-none font-sans text-xs text-themeTextSecondary">
            {suggestions.map((s, idx) => (
              <div
                key={`${s.type}-${s.value}`}
                onClick={() => handleSelect(s)}
                className={`px-3.5 py-2 cursor-pointer flex items-center justify-between transition-colors ${
                  idx === focusedIndex
                    ? 'bg-themeAccentBg text-themeAccent'
                    : 'hover:bg-themeAccentBg hover:text-themeAccent text-themeTextSecondary'
                }`}
              >
                <span className="truncate font-medium">{s.value}</span>
                <span className="text-[9px] font-bold text-themeTextMuted uppercase tracking-wider ml-2 bg-themeBgPrimary px-1.5 py-0.5 rounded border border-themeBorder">
                  {s.type}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {onOpenHelp && (
        <button
          onClick={onOpenHelp}
          className={`shrink-0 p-1.5 rounded-lg text-themeTextMuted ${accentHoverClass} hover:bg-themeAccentBg transition-colors`}
          title="Filter syntax help"
        >
          <HelpCircle size={14} />
        </button>
      )}
    </div>
  );
};
