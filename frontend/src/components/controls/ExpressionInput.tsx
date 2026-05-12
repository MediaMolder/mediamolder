// Syntax-highlighted, live-validating expression input for AVOption
// fields whose value is parsed by libavutil's expression evaluator
// (Wave 5 #20). Renders a transparent <textarea> over a styled <pre>
// to get colourised tokens without pulling in Monaco / CodeMirror.
//
// Validation is debounced and POSTs (well, GETs — the backend
// endpoint is GET) against /api/filters/{filter}/eval-expression
// with `expr` and any extra variable bindings the caller supplies.
// The endpoint returns `{ ok, value, error }`; we surface `error`
// inline and the numeric `value` (under default 0 bindings) as a
// hint so users can preview the result of e.g. `between(t,1,8)` at
// `t=0`.
//
// Cookbook patterns are inserted at the cursor position, replacing
// any selected text.

import { useEffect, useId, useMemo, useRef, useState } from 'react';

interface Props {
  filter: string;
  option: string;
  value: string;
  variables?: string[];
  /** Numeric bindings for the filter's expression variables derived from
   * the upstream pad (width, height, fps, …). Passed as extra query
   * params to the eval endpoint so the live preview shows context-aware
   * results instead of the default all-zero bindings. */
  padHints?: Record<string, number>;
  onChange: (next: string) => void;
  placeholder?: string;
}

interface EvalResp {
  filter: string;
  expr: string;
  variables: Record<string, number>;
  ok: boolean;
  value?: number;
  error?: string;
}

// Top-5 cookbook patterns referenced in the roadmap §6.5 #20:
// between / scroll / frame-stamp / fade-gate / conditional.
interface Cookbook {
  label: string;
  expr: string;
  hint: string;
}

const COOKBOOK: Cookbook[] = [
  // ── Timeline gates ───────────────────────────────────────────────────
  {
    label: 'timeline gate: between(t, A, B)',
    expr: 'between(t,1,8)',
    hint: 'Truthy while t ∈ [1,8] s — wire to enable=.',
  },
  {
    label: 'enable after timestamp',
    expr: 'gt(t,30)',
    hint: 'Enabled from 30 s onward.',
  },
  {
    label: 'disable (mute) in range',
    expr: 'not(between(t,2,5))',
    hint: 'Enabled everywhere except [2,5] s.',
  },
  // ── Speed / PTS ──────────────────────────────────────────────────────
  {
    label: 'setpts: 2× speed',
    expr: '0.5*PTS',
    hint: 'setpts: halve PTS → double playback speed.',
  },
  {
    label: 'setpts: 0.5× slow-mo',
    expr: '2*PTS',
    hint: 'setpts: double PTS → half speed (slow motion).',
  },
  // ── Text overlay (drawtext) ───────────────────────────────────────────
  {
    label: 'drawtext: center X',
    expr: '(main_w-tw)/2',
    hint: 'drawtext.x: horizontally centred text.',
  },
  {
    label: 'drawtext: bottom-center Y',
    expr: 'main_h-line_h-10',
    hint: 'drawtext.y: 10 px above the bottom edge.',
  },
  {
    label: 'drawtext: top-right X',
    expr: 'main_w-tw-10',
    hint: 'drawtext.x: flush right with a 10 px margin.',
  },
  {
    label: 'drawtext: scrolling marquee',
    expr: 'w-mod(40*t,w+tw)',
    hint: 'drawtext.x: scrolls text right-to-left at 40 px/s.',
  },
  // ── Force-keyframe expression ─────────────────────────────────────────
  {
    label: 'force key every 2 s',
    expr: 'expr:gte(t,n_forced*2)',
    hint: 'Output.ForceKeyFrames: I-frame every 2 s.',
  },
  // ── Overlay / compositing ─────────────────────────────────────────────
  {
    label: 'overlay: center X',
    expr: '(main_w-overlay_w)/2',
    hint: 'overlay.x: horizontally centred overlay.',
  },
  {
    label: 'overlay: center Y',
    expr: '(main_h-overlay_h)/2',
    hint: 'overlay.y: vertically centred overlay.',
  },
  // ── Crop / pad ────────────────────────────────────────────────────────
  {
    label: 'crop/pad: center X',
    expr: '(in_w-out_w)/2',
    hint: 'crop.x / pad.x: centred horizontal offset.',
  },
  {
    label: 'crop/pad: center Y',
    expr: '(in_h-out_h)/2',
    hint: 'crop.y / pad.y: centred vertical offset.',
  },
  // ── Volume / audio ────────────────────────────────────────────────────
  {
    label: 'volume: 3 s fade-in',
    expr: 'if(lt(t,3),t/3,1)',
    hint: 'volume.volume: ramp from silence to 0 dB over 3 s.',
  },
  {
    label: 'volume/alpha: fade in then out',
    expr: 'if(lt(t,1),t,if(lt(t,4),1,if(lt(t,5),5-t,0)))',
    hint: '0→1 over [0,1]s, hold until 4 s, 1→0 over [4,5]s.',
  },
  // ── Frame selection ───────────────────────────────────────────────────
  {
    label: 'select: keyframes only',
    expr: 'eq(pict_type,PICT_TYPE_I)',
    hint: 'select.expr: pass only I-frames.',
  },
  {
    label: 'select: every 5th frame',
    expr: 'if(eq(mod(n,5),0),1,0)',
    hint: 'select.expr: keep frames n = 0, 5, 10, …',
  },
  // ── zoompan ───────────────────────────────────────────────────────────
  {
    label: 'zoompan: Ken Burns slow zoom',
    expr: 'min(zoom+0.0015,1.5)',
    hint: 'zoompan.zoom: gradual zoom from 1× to 1.5× over ~333 frames.',
  },
];

// Tokeniser: identifiers (function or var), numbers, operators,
// parens/commas, and the rest. Escaped backslashes (libavfilter
// argument-quoting) are preserved as part of the next token.
const TOKEN_RE =
  /([A-Za-z_][A-Za-z_0-9]*)|([0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)|(\\.|[+\-*/%^!=<>(),])/g;

const KNOWN_FUNCS = new Set([
  'abs', 'acos', 'asin', 'atan', 'atan2', 'between', 'bitand', 'bitor', 'ceil',
  'clip', 'cos', 'cosh', 'eq', 'exp', 'floor', 'fmod', 'gauss', 'gcd', 'gt',
  'gte', 'hypot', 'if', 'ifnot', 'isinf', 'isnan', 'ld', 'log', 'lt', 'lte',
  'max', 'min', 'mod', 'not', 'pow', 'print', 'random', 'root', 'round', 'sgn',
  'sin', 'sinh', 'sqrt', 'squish', 'st', 'tan', 'tanh', 'taylor', 'time',
  'trunc', 'while',
]);

/** Find the identifier word surrounding `pos` in `text`. */
function wordAtCursor(
  text: string,
  pos: number,
): { word: string; start: number; end: number } {
  let s = pos;
  while (s > 0 && /[A-Za-z_0-9]/.test(text[s - 1])) s--;
  let e = pos;
  while (e < text.length && /[A-Za-z_0-9]/.test(text[e])) e++;
  return { word: text.slice(s, e), start: s, end: e };
}

export function ExpressionInput({
  filter,
  option,
  value,
  variables,
  padHints,
  onChange,
  placeholder,
}: Props) {
  const [evalResp, setEvalResp] = useState<EvalResp | null>(null);
  const [evalErr, setEvalErr] = useState<string | null>(null);
  // Autocomplete state
  const [acItems, setAcItems] = useState<string[]>([]);
  const [acIndex, setAcIndex] = useState(0);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const preRef = useRef<HTMLPreElement>(null);
  const id = useId();

  const knownVars = useMemo(() => new Set(variables ?? []), [variables]);

  // Sorted candidate pool for autocomplete: filter variables first, then
  // built-in libavutil functions.
  const allCompletions = useMemo(() => {
    const vars = Array.from(knownVars).sort().map((v) => ({ text: v, isVar: true }));
    const fns = Array.from(KNOWN_FUNCS).sort().map((f) => ({ text: f, isVar: false }));
    return [...vars, ...fns];
  }, [knownVars]);

  // Debounced eval. Skip empty. Thread padHints as extra variable bindings
  // so the preview uses realistic values when upstream pad info is available.
  useEffect(() => {
    const trimmed = value.trim();
    if (!trimmed) {
      setEvalResp(null);
      setEvalErr(null);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(() => {
      const params = new URLSearchParams({ expr: trimmed });
      if (padHints) {
        for (const [k, v] of Object.entries(padHints)) params.set(k, String(v));
      }
      const url = `/api/filters/${encodeURIComponent(filter)}/eval-expression?${params.toString()}`;
      fetch(url, { signal: ctl.signal })
        .then(async (r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`);
          return (await r.json()) as EvalResp;
        })
        .then((j) => {
          setEvalResp(j);
          setEvalErr(null);
        })
        .catch((e: Error) => {
          if (e.name !== 'AbortError') setEvalErr(e.message);
        });
    }, 250);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [filter, value, padHints]);

  // Mirror textarea scroll into the highlight overlay so long lines
  // stay aligned.
  const handleScroll = () => {
    if (taRef.current && preRef.current) {
      preRef.current.scrollTop = taRef.current.scrollTop;
      preRef.current.scrollLeft = taRef.current.scrollLeft;
    }
  };

  const insertCookbook = (snippet: string) => {
    const ta = taRef.current;
    if (!ta) {
      onChange(snippet);
      return;
    }
    const start = ta.selectionStart ?? value.length;
    const end = ta.selectionEnd ?? value.length;
    const next = value.slice(0, start) + snippet + value.slice(end);
    onChange(next);
    requestAnimationFrame(() => {
      ta.focus();
      const pos = start + snippet.length;
      ta.setSelectionRange(pos, pos);
    });
  };

  // Replace the partial word at cursor with the chosen completion.
  const applyCompletion = (completion: string) => {
    const ta = taRef.current;
    if (!ta) return;
    const pos = ta.selectionStart ?? value.length;
    const { start, end } = wordAtCursor(value, pos);
    const next = value.slice(0, start) + completion + value.slice(end);
    onChange(next);
    setAcItems([]);
    requestAnimationFrame(() => {
      ta.focus();
      const newPos = start + completion.length;
      ta.setSelectionRange(newPos, newPos);
    });
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const newVal = e.target.value;
    onChange(newVal);
    const pos = e.target.selectionStart ?? newVal.length;
    const { word } = wordAtCursor(newVal, pos);
    if (word.length >= 1) {
      const q = word.toLowerCase();
      const matches = allCompletions
        .filter((c) => c.text.toLowerCase().startsWith(q) && c.text !== word)
        .map((c) => c.text);
      setAcItems(matches.slice(0, 12));
      setAcIndex(0);
    } else {
      setAcItems([]);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (acItems.length === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setAcIndex((i) => Math.min(i + 1, acItems.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setAcIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Tab' || e.key === 'Enter') {
      e.preventDefault();
      applyCompletion(acItems[acIndex]);
    } else if (e.key === 'Escape') {
      setAcItems([]);
    }
  };

  // Dismiss completions on blur, but defer so a click on a completion
  // item fires its onMouseDown before the list disappears.
  const handleBlur = () => setTimeout(() => setAcItems([]), 100);

  const tokens = highlight(value, knownVars);
  const hasPadCtx = padHints != null && Object.keys(padHints).length > 0;
  const status = evalErr
    ? { kind: 'err' as const, msg: evalErr }
    : evalResp
      ? evalResp.ok
        ? {
            kind: 'ok' as const,
            msg: `= ${formatNum(evalResp.value)} (${hasPadCtx ? 'from context' : 'vars=0'})`,
          }
        : { kind: 'err' as const, msg: evalResp.error || 'invalid expression' }
      : null;

  return (
    <div className="expr-input">
      <div className="expr-input-frame">
        <pre ref={preRef} className="expr-highlight" aria-hidden="true">
          {tokens}
          {/* Trailing newline keeps the overlay tall enough when the
              textarea wraps via its own scrollbar. */}
          {'\n'}
        </pre>
        <textarea
          ref={taRef}
          id={id}
          className="expr-textarea"
          value={value}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          rows={2}
          placeholder={placeholder}
          onChange={handleChange}
          onKeyDown={handleKeyDown}
          onBlur={handleBlur}
          onScroll={handleScroll}
        />
        {acItems.length > 0 && (
          <div className="expr-completions" role="listbox" aria-label="Completions">
            {acItems.map((item, i) => (
              <div
                key={item}
                role="option"
                aria-selected={i === acIndex}
                className={`expr-completion-item${i === acIndex ? ' selected' : ''}`}
                onMouseDown={(e) => {
                  e.preventDefault(); // prevent textarea blur before apply
                  applyCompletion(item);
                }}
              >
                <span className={knownVars.has(item) ? 'tok-var' : 'tok-fn'}>{item}</span>
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="expr-meta">
        <select
          aria-label={`Cookbook patterns for ${option}`}
          value=""
          onChange={(e) => {
            const c = COOKBOOK.find((x) => x.label === e.target.value);
            if (c) insertCookbook(c.expr);
            e.currentTarget.value = '';
          }}
        >
          <option value="">Expression examples…</option>
          {COOKBOOK.map((c) => (
            <option key={c.label} value={c.label} title={c.hint}>
              {c.label}
            </option>
          ))}
        </select>
        {status && (
          <span className={status.kind === 'ok' ? 'expr-ok' : 'expr-err'}>
            {status.msg}
          </span>
        )}
      </div>
      <style>{styles}</style>
    </div>
  );
}

function formatNum(n: number | undefined): string {
  if (n === undefined || !Number.isFinite(n)) return String(n);
  if (Number.isInteger(n)) return String(n);
  return n.toFixed(4).replace(/0+$/, '').replace(/\.$/, '');
}

// highlight returns React nodes representing the tokenised input.
// Functions/variables/numbers/operators get dedicated classes; the
// known-vars set comes from the AVOption schema so the user gets
// immediate "unknown identifier" feedback without a round-trip.
function highlight(src: string, knownVars: Set<string>) {
  if (!src) return null;
  const out: React.ReactNode[] = [];
  let i = 0;
  let key = 0;
  TOKEN_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = TOKEN_RE.exec(src)) !== null) {
    if (m.index > i) out.push(src.slice(i, m.index));
    const [tok, ident, num, op] = m;
    if (ident !== undefined) {
      const cls = KNOWN_FUNCS.has(ident)
        ? 'tok-fn'
        : knownVars.has(ident)
          ? 'tok-var'
          : 'tok-unknown';
      out.push(
        <span key={key++} className={cls} title={cls === 'tok-unknown' ? 'Unknown identifier' : undefined}>
          {tok}
        </span>,
      );
    } else if (num !== undefined) {
      out.push(<span key={key++} className="tok-num">{tok}</span>);
    } else if (op !== undefined) {
      out.push(<span key={key++} className="tok-op">{tok}</span>);
    } else {
      out.push(tok);
    }
    i = m.index + tok.length;
  }
  if (i < src.length) out.push(src.slice(i));
  return out;
}

const styles = `
.expr-input { display: flex; flex-direction: column; gap: 4px; }
.expr-input-frame { position: relative; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; line-height: 1.4; }
.expr-highlight, .expr-textarea {
  margin: 0; padding: 6px 8px;
  font-family: inherit; font-size: inherit; line-height: inherit;
  border: 1px solid #444; border-radius: 4px;
  white-space: pre-wrap; word-break: break-all;
  min-height: 40px; box-sizing: border-box; width: 100%;
}
.expr-highlight {
  position: absolute; inset: 0;
  background: #1e1e1e; color: #ddd;
  pointer-events: none; overflow: hidden;
}
.expr-textarea {
  position: relative;
  background: transparent; color: transparent; caret-color: #fff;
  resize: vertical; outline: none;
}
.expr-textarea:focus { border-color: #5b8def; }
.tok-fn { color: #c586c0; }
.tok-var { color: #4ec9b0; }
.tok-unknown { color: #f5b7b1; text-decoration: underline wavy #f5b7b1; }
.tok-num { color: #b5cea8; }
.tok-op { color: #d4d4d4; }
.expr-meta { display: flex; gap: 8px; align-items: center; font-size: 11px; }
.expr-meta select { font-size: 11px; padding: 2px 4px; }
.expr-ok { color: #6cc06c; }
.expr-err { color: #f5b7b1; }
.expr-completions {
  position: absolute; top: 100%; left: 0; right: 0; z-index: 100;
  background: #252526; border: 1px solid #555; border-top: none;
  border-radius: 0 0 4px 4px; max-height: 180px; overflow-y: auto;
  box-shadow: 0 4px 8px rgba(0,0,0,0.4);
}
.expr-completion-item {
  padding: 3px 8px; cursor: pointer; font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: nowrap; user-select: none;
}
.expr-completion-item:hover, .expr-completion-item.selected { background: #094771; }
`;
