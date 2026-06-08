import React, { useState } from 'react';
import { X, Plus, Trash2, Check, Palette } from 'lucide-react';
import { formatThemeName } from '../utils';

export interface ThemeColors {
  bg_primary: string;
  bg_secondary: string;
  text_primary: string;
  text_secondary: string;
  text_muted: string;
  border_color: string;
  accent_primary: string;
  accent_secondary: string;
  accent_hover: string;
  accent_bg: string;
}

export interface ThemeSchedule {
  start_month: number;
  start_day: number;
  end_month: number;
  end_day: number;
}

export interface Theme {
  name: string;
  default_mode: 'light' | 'dark';
  light: ThemeColors;
  dark: ThemeColors;
  custom: boolean;
  schedule?: ThemeSchedule;
}

interface ThemeManagerModalProps {
  isOpen: boolean;
  onClose: () => void;
  themes: Theme[];
  activeThemeName: string;
  onSelectTheme: (name: string) => void;
  onSaveTheme: (theme: Theme) => Promise<void>;
  onDeleteTheme: (name: string) => Promise<void>;
}

const formatMonthName = (month: number): string => {
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return months[month - 1] || '';
};


const colorFields: { key: keyof ThemeColors; label: string; desc: string }[] = [
  { key: 'bg_primary', label: 'Primary Background', desc: 'Main window backdrop background' },
  { key: 'bg_secondary', label: 'Secondary Background', desc: 'Sidebar and content panel cards' },
  { key: 'text_primary', label: 'Primary Text', desc: 'Main article titles and headers' },
  { key: 'text_secondary', label: 'Secondary Text', desc: 'Article body copy and text' },
  { key: 'text_muted', label: 'Muted Text', desc: 'Dates, disabled elements, and labels' },
  { key: 'border_color', label: 'Border Color', desc: 'Dividers, lines, and container borders' },
  { key: 'accent_primary', label: 'Primary Accent', desc: 'WikiLinks, active buttons, and highlights' },
  { key: 'accent_secondary', label: 'Secondary Accent', desc: 'Tag badges and glowing indicators' },
  { key: 'accent_hover', label: 'Accent Hover', desc: 'Buttons and links on hover state' },
  { key: 'accent_bg', label: 'Accent Background', desc: 'Blockquotes and inline code boxes background' },
];

const emptyColors = (): ThemeColors => ({
  bg_primary: '#f8fafc',
  bg_secondary: '#ffffff',
  text_primary: '#0f172a',
  text_secondary: '#334155',
  text_muted: '#64748b',
  border_color: '#e2e8f0',
  accent_primary: '#4f46e5',
  accent_secondary: '#7c3aed',
  accent_hover: '#3730a3',
  accent_bg: '#e0e7ff',
});

export const ThemeManagerModal: React.FC<ThemeManagerModalProps> = ({
  isOpen,
  onClose,
  themes,
  activeThemeName,
  onSelectTheme,
  onSaveTheme,
  onDeleteTheme,
}) => {
  const [isCreating, setIsCreating] = useState(false);
  const [editThemeName, setEditThemeName] = useState('');
  const [editDefaultMode, setEditDefaultMode] = useState<'light' | 'dark'>('light');
  const [schedStartMonth, setSchedStartMonth] = useState('1');
  const [schedStartDay, setSchedStartDay] = useState('1');
  const [schedEndMonth, setSchedEndMonth] = useState('12');
  const [schedEndDay, setSchedEndDay] = useState('31');
  const [schedEnabled, setSchedEnabled] = useState(false);
  const [editLightColors, setEditLightColors] = useState<ThemeColors>(emptyColors());
  const [editDarkColors, setEditDarkColors] = useState<ThemeColors>({
    bg_primary: '#020617',
    bg_secondary: '#0f172a',
    text_primary: '#f8fafc',
    text_secondary: '#cbd5e1',
    text_muted: '#64748b',
    border_color: '#1e293b',
    accent_primary: '#4f46e5',
    accent_secondary: '#10b981',
    accent_hover: '#6366f1',
    accent_bg: 'rgba(49, 46, 129, 0.2)',
  });
  const [activeEditorTab, setActiveEditorTab] = useState<'light' | 'dark'>('light');
  const [errorMsg, setErrorMsg] = useState('');

  if (!isOpen) return null;

  const handleStartCreate = () => {
    setIsCreating(true);
    setEditThemeName('');
    setEditDefaultMode('light');
    setEditLightColors(emptyColors());
    setEditDarkColors({
      bg_primary: '#020617',
      bg_secondary: '#0f172a',
      text_primary: '#f8fafc',
      text_secondary: '#cbd5e1',
      text_muted: '#64748b',
      border_color: '#1e293b',
      accent_primary: '#4f46e5',
      accent_secondary: '#10b981',
      accent_hover: '#6366f1',
      accent_bg: 'rgba(49, 46, 129, 0.2)',
    });
    setActiveEditorTab('light');
    setErrorMsg('');
    setSchedEnabled(false);
    setSchedStartMonth('1');
    setSchedStartDay('1');
    setSchedEndMonth('12');
    setSchedEndDay('31');
  };

  const handleColorChange = (tab: 'light' | 'dark', key: keyof ThemeColors, value: string) => {
    if (tab === 'light') {
      setEditLightColors((prev) => ({ ...prev, [key]: value }));
    } else {
      setEditDarkColors((prev) => ({ ...prev, [key]: value }));
    }
  };

  const handleSave = async (e: React.SyntheticEvent) => {
    e.preventDefault();
    setErrorMsg('');

    const trimmedName = editThemeName.trim();
    if (!trimmedName) {
      setErrorMsg('Theme name is required.');
      return;
    }

    if (trimmedName.toLowerCase() === 'default') {
      setErrorMsg('Cannot use reserved name "default".');
      return;
    }

    const themeToSave: Theme = {
      name: trimmedName,
      default_mode: editDefaultMode,
      light: editLightColors,
      dark: editDarkColors,
      custom: true,
    };

    if (schedEnabled) {
      themeToSave.schedule = {
        start_month: parseInt(schedStartMonth, 10) || 1,
        start_day: parseInt(schedStartDay, 10) || 1,
        end_month: parseInt(schedEndMonth, 10) || 12,
        end_day: parseInt(schedEndDay, 10) || 31,
      };
    }

    try {
      await onSaveTheme(themeToSave);
      setIsCreating(false);
      onSelectTheme(trimmedName);
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : 'Failed to save theme.');
    }
  };

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-slate-950/60 backdrop-blur-sm animate-fade-in no-print">
      <div 
        className="w-full max-w-4xl h-[85vh] flex flex-col rounded-3xl theme-card shadow-2xl overflow-hidden animate-slide-up"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Modal Header */}
        <div className="p-6 border-b border-themeBorder flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-xl bg-themeAccentBg text-themeAccent">
              <Palette size={20} />
            </div>
            <div>
              <h2 className="text-lg font-bold text-themeTextPrimary">Wiki Theme Manager</h2>
              <p className="text-xs text-themeTextMuted">Select active appearance or craft customized dual-mode themes</p>
            </div>
          </div>
          <button 
            onClick={onClose}
            className="p-2 rounded-xl text-themeTextMuted hover:bg-themeBgPrimary hover:text-themeTextPrimary transition-all active:scale-95"
          >
            <X size={18} />
          </button>
        </div>

        {/* Modal Body Container */}
        <div className="flex-1 flex overflow-hidden">
          
          {/* Left Sidebar: Themes List */}
          <div className="w-1/3 border-r border-themeBorder flex flex-col bg-themeBgPrimary/40">
            <div className="p-4 flex-1 overflow-y-auto space-y-2">
              <span className="text-[10px] font-extrabold text-themeTextMuted uppercase tracking-wider block px-2">Available Themes</span>
              
              {themes.map((theme) => {
                const isActive = theme.name === activeThemeName;
                return (
                  <div
                    key={theme.name}
                    onClick={() => {
                      onSelectTheme(theme.name);
                      setIsCreating(false);
                    }}
                    className={`group flex items-center justify-between p-3.5 rounded-xl cursor-pointer transition-all border ${
                      isActive
                        ? 'bg-themeAccentBg/40 border-themeAccent text-themeAccent font-bold'
                        : 'bg-themeBgSecondary border-themeBorder hover:bg-themeBgSecondary/80 text-themeTextSecondary'
                    }`}
                  >
                    <div className="flex flex-col gap-0.5 overflow-hidden">
                      <span className="text-sm truncate">{formatThemeName(theme.name)}</span>
                      <span className="text-[10px] opacity-80 font-normal">
                        Default: {theme.default_mode} mode
                      </span>
                      {theme.schedule && (
                        <span className="inline-flex items-center gap-1 mt-1.5 text-[9px] font-bold px-2 py-0.5 rounded-full bg-themeAccentBg/40 border border-themeAccent/20 text-themeAccent max-w-fit select-none">
                          {theme.name === 'halloween' && '🎃 '}
                          {theme.name === 'christmas' && '🎄 '}
                          {theme.name === 'independence-day' && '🎆 '}
                          {theme.name === 'new-years' && '✨ '}
                          {!['halloween', 'christmas', 'independence-day', 'new-years'].includes(theme.name) && '📅 '}
                          {formatMonthName(theme.schedule.start_month)} {theme.schedule.start_day} - {formatMonthName(theme.schedule.end_month)} {theme.schedule.end_day}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      {isActive && <Check size={14} className="text-themeAccent shrink-0" />}
                      {theme.custom && (
                        <button
                          onClick={async (e) => {
                            e.stopPropagation();
                            if (confirm(`Are you sure you want to permanently delete custom theme "${theme.name}"?`)) {
                              await onDeleteTheme(theme.name);
                            }
                          }}
                          title="Delete Custom Theme"
                          className="p-1 rounded text-rose-500 hover:bg-rose-50 dark:hover:bg-rose-950/30 opacity-0 group-hover:opacity-100 transition-opacity"
                        >
                          <Trash2 size={13} />
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>

            <div className="p-4 border-t border-themeBorder bg-themeBgSecondary/30">
              <button
                onClick={handleStartCreate}
                className="w-full flex items-center justify-center gap-2 py-2.5 px-4 rounded-xl bg-themeAccent hover:opacity-90 active:scale-95 text-white font-semibold text-xs shadow-md transition-all"
              >
                <Plus size={14} />
                <span>Create Custom Theme</span>
              </button>
            </div>
          </div>

          {/* Right Area: Theme Editor / Preview Panel */}
          <div className="flex-1 flex flex-col bg-themeBgSecondary">
            {isCreating ? (
              // Theme Creation Form
              <form onSubmit={handleSave} className="flex-1 flex flex-col overflow-hidden">
                <div className="flex-1 overflow-y-auto p-6 space-y-6">
                  <h3 className="text-base font-bold text-themeTextPrimary">Create New Custom Theme</h3>
                  
                  {errorMsg && (
                    <div className="p-3 text-xs bg-rose-50 dark:bg-rose-950/20 border border-rose-200 dark:border-rose-900 rounded-xl text-rose-600 dark:text-rose-400 font-semibold animate-pulse">
                      {errorMsg}
                    </div>
                  )}

                  {/* Form Field: Name */}
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-themeTextSecondary">Theme Name</label>
                    <input
                      type="text"
                      value={editThemeName}
                      onChange={(e) => setEditThemeName(e.target.value)}
                      placeholder="e.g. Emerald Forest, Solar Breeze"
                      className="w-full p-2.5 text-xs rounded-xl bg-themeBgPrimary/50 border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary"
                    />
                  </div>

                  {/* Form Field: Default Variant */}
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-themeTextSecondary">Default Active Mode</label>
                    <div className="flex gap-2">
                      <button
                        type="button"
                        onClick={() => setEditDefaultMode('light')}
                        className={`flex-1 py-2 px-3 text-xs rounded-xl border font-semibold transition-all ${
                          editDefaultMode === 'light'
                            ? 'bg-themeAccent text-white border-themeAccent shadow-sm'
                            : 'bg-themeBgPrimary border-themeBorder text-themeTextSecondary hover:bg-themeBgPrimary/80'
                        }`}
                      >
                        Light Mode Default
                      </button>
                      <button
                        type="button"
                        onClick={() => setEditDefaultMode('dark')}
                        className={`flex-1 py-2 px-3 text-xs rounded-xl border font-semibold transition-all ${
                          editDefaultMode === 'dark'
                            ? 'bg-themeAccent text-white border-themeAccent shadow-sm'
                            : 'bg-themeBgPrimary border-themeBorder text-themeTextSecondary hover:bg-themeBgPrimary/80'
                        }`}
                      >
                        Dark Mode Default
                      </button>
                    </div>
                  </div>

                  {/* Theme Schedule (Optional) */}
                  <div className="p-4 bg-themeBgPrimary/30 rounded-2xl border border-themeBorder/40 space-y-4">
                    <div className="flex items-center justify-between">
                      <div className="flex flex-col">
                        <span className="text-xs font-bold text-themeTextPrimary">Enable Theme Schedule</span>
                        <span className="text-[10px] text-themeTextMuted">Auto-apply this theme during a specific date range</span>
                      </div>
                      <input
                        type="checkbox"
                        checked={schedEnabled}
                        onChange={(e) => setSchedEnabled(e.target.checked)}
                        className="w-4 h-4 text-themeAccent bg-themeBgPrimary border-themeBorder rounded focus:ring-themeAccent cursor-pointer"
                      />
                    </div>

                    {schedEnabled && (
                      <div className="grid grid-cols-2 gap-4 animate-fade-in">
                        <div className="space-y-1.5">
                          <label className="text-[10px] font-bold text-themeTextMuted">Start Window</label>
                          <div className="flex gap-1.5">
                            <select
                              value={schedStartMonth}
                              onChange={(e) => setSchedStartMonth(e.target.value)}
                              className="flex-1 p-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary"
                            >
                              {Array.from({ length: 12 }, (_, i) => (
                                <option key={i + 1} value={i + 1}>
                                  {formatMonthName(i + 1)}
                                </option>
                              ))}
                            </select>
                            <input
                              type="number"
                              min="1"
                              max="31"
                              value={schedStartDay}
                              onChange={(e) => setSchedStartDay(e.target.value)}
                              className="w-16 p-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary text-center"
                              placeholder="Day"
                            />
                          </div>
                        </div>

                        <div className="space-y-1.5">
                          <label className="text-[10px] font-bold text-themeTextMuted">End Window (Inclusive)</label>
                          <div className="flex gap-1.5">
                            <select
                              value={schedEndMonth}
                              onChange={(e) => setSchedEndMonth(e.target.value)}
                              className="flex-1 p-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary"
                            >
                              {Array.from({ length: 12 }, (_, i) => (
                                <option key={i + 1} value={i + 1}>
                                  {formatMonthName(i + 1)}
                                </option>
                              ))}
                            </select>
                            <input
                              type="number"
                              min="1"
                              max="31"
                              value={schedEndDay}
                              onChange={(e) => setSchedEndDay(e.target.value)}
                              className="w-16 p-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary text-center"
                              placeholder="Day"
                            />
                          </div>
                        </div>
                      </div>
                    )}
                  </div>

                  {/* Color Customizers Tabs */}
                  <div className="space-y-3 pt-2">
                    <div className="flex border-b border-themeBorder">
                      <button
                        type="button"
                        onClick={() => setActiveEditorTab('light')}
                        className={`py-2 px-4 text-xs font-bold border-b-2 transition-all ${
                          activeEditorTab === 'light'
                            ? 'border-themeAccent text-themeAccent'
                            : 'border-transparent text-themeTextMuted hover:text-themeTextPrimary'
                        }`}
                      >
                        ☀️ Light Variant Colors
                      </button>
                      <button
                        type="button"
                        onClick={() => setActiveEditorTab('dark')}
                        className={`py-2 px-4 text-xs font-bold border-b-2 transition-all ${
                          activeEditorTab === 'dark'
                            ? 'border-themeAccent text-themeAccent'
                            : 'border-transparent text-themeTextMuted hover:text-themeTextPrimary'
                        }`}
                      >
                        🌙 Dark Variant Colors
                      </button>
                    </div>

                    {/* Color Input Grid */}
                    <div className="grid grid-cols-2 gap-4">
                      {colorFields.map((field) => {
                        const curVal = activeEditorTab === 'light' 
                          ? editLightColors[field.key] 
                          : editDarkColors[field.key];
                        return (
                          <div key={field.key} className="flex items-center gap-3 p-2.5 rounded-xl border border-themeBorder bg-themeBgPrimary/20">
                            <div className="relative w-8 h-8 rounded-lg overflow-hidden border border-themeBorder shrink-0">
                              <input
                                type="color"
                                value={curVal.startsWith('#') && curVal.length === 7 ? curVal : '#ffffff'}
                                onChange={(e) => handleColorChange(activeEditorTab, field.key, e.target.value)}
                                className="absolute inset-0 w-full h-full scale-150 cursor-pointer p-0 border-none bg-transparent"
                              />
                            </div>
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center justify-between">
                                <span className="text-[11px] font-bold text-themeTextSecondary truncate">{field.label}</span>
                                <input
                                  type="text"
                                  value={curVal}
                                  onChange={(e) => handleColorChange(activeEditorTab, field.key, e.target.value)}
                                  className="text-[9px] font-mono text-themeTextMuted border-none bg-transparent w-16 text-right p-0 focus:outline-none focus:text-themeAccent"
                                />
                              </div>
                              <span className="text-[9px] text-themeTextMuted block truncate leading-none mt-0.5">{field.desc}</span>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                </div>

                {/* Form Footer */}
                <div className="p-4 border-t border-themeBorder bg-themeBgPrimary/40 flex items-center justify-end gap-2 shrink-0">
                  <button
                    type="button"
                    onClick={() => setIsCreating(false)}
                    className="py-2 px-4 rounded-xl border border-themeBorder hover:bg-themeBgPrimary text-themeTextSecondary text-xs font-semibold active:scale-95 transition-all"
                  >
                    Cancel
                  </button>
                  <button
                    type="submit"
                    className="py-2 px-5 rounded-xl bg-themeAccent text-white text-xs font-bold hover:opacity-90 active:scale-95 transition-all shadow-md"
                  >
                    Save Custom Theme
                  </button>
                </div>
              </form>
            ) : (
              // Selected Theme Preview and Action
              <div className="flex-1 flex flex-col p-6 space-y-6 overflow-y-auto">
                {(() => {
                  const selectedTheme = themes.find(t => t.name === activeThemeName) || themes[0];
                  if (!selectedTheme) return null;
                  return (
                    <>
                      <div className="space-y-1">
                        <div className="flex items-center gap-2">
                          <h3 className="text-base font-black text-themeTextPrimary">{formatThemeName(selectedTheme.name)} Theme</h3>
                          <span className="text-[9px] font-extrabold uppercase px-1.5 py-0.5 rounded bg-themeAccentBg text-themeAccent border border-themeAccent/20">
                            {selectedTheme.custom ? 'Custom' : 'System Theme'}
                          </span>
                        </div>
                        <p className="text-xs text-themeTextMuted">
                          Default Variant: {selectedTheme.default_mode === 'light' ? '☀️ Light' : '🌙 Dark'} Mode
                        </p>
                      </div>

                      {/* Dual-Mode Colors Preview Panel */}
                      <div className="space-y-4 flex-1">
                        <span className="text-[10px] font-extrabold text-themeTextMuted uppercase tracking-wider block">Palette Colors Matrix</span>
                        
                        <div className="grid grid-cols-2 gap-6">
                          
                          {/* Light Variant Preview Card */}
                          <div className="border border-themeBorder rounded-2xl p-4 bg-white space-y-3">
                            <span className="text-[10px] font-extrabold text-slate-400 uppercase tracking-wider block border-b pb-1">☀️ Light Variant Colors</span>
                            <div className="grid grid-cols-2 gap-2 text-[10px] text-slate-600">
                              {Object.entries(selectedTheme.light).map(([key, val]) => (
                                <div key={key} className="flex items-center gap-1.5 truncate">
                                  <span 
                                    className="w-3.5 h-3.5 rounded border border-slate-200 shrink-0" 
                                    style={{ backgroundColor: val }}
                                  />
                                  <span className="truncate leading-none">{key.replace('_', ' ')}</span>
                                </div>
                              ))}
                            </div>
                          </div>

                          {/* Dark Variant Preview Card */}
                          <div className="border border-themeBorder rounded-2xl p-4 bg-slate-950 space-y-3">
                            <span className="text-[10px] font-extrabold text-slate-600 uppercase tracking-wider block border-b border-slate-800 pb-1">🌙 Dark Variant Colors</span>
                            <div className="grid grid-cols-2 gap-2 text-[10px] text-slate-400">
                              {Object.entries(selectedTheme.dark).map(([key, val]) => (
                                <div key={key} className="flex items-center gap-1.5 truncate">
                                  <span 
                                    className="w-3.5 h-3.5 rounded border border-slate-800 shrink-0" 
                                    style={{ backgroundColor: val }}
                                  />
                                  <span className="truncate leading-none">{key.replace('_', ' ')}</span>
                                </div>
                              ))}
                            </div>
                          </div>

                        </div>
                      </div>

                      <div className="p-4 border border-themeBorder rounded-2xl bg-themeBgPrimary/40 flex items-center justify-between text-xs text-themeTextSecondary font-medium">
                        <span>Select this theme in the sidebar to activate the color configurations!</span>
                        <button
                          onClick={() => {
                            onSelectTheme(selectedTheme.name);
                            onClose();
                          }}
                          className="py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-bold active:scale-95 transition-all shadow-sm"
                        >
                          Activate Theme
                        </button>
                      </div>
                    </>
                  );
                })()}
              </div>
            )}

          </div>

        </div>

      </div>
    </div>
  );
};
