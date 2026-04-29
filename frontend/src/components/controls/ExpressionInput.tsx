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
  {
    label: 'between (timeline gate)',
    expr: 'between(t,1,8)',
    hint: 'Truthy while t is in [1,8] seconds — feed to enable=.',
  },
  {
    label: 'scroll (horizontal marquee)',
    expr: 'w-mod(40*t\\,w+tw)',
    hint: 'drawtext.x: scrolls text right-to-left at 40 px/s.',
  },
  {
    label: 'frame-stamp (every 2s key)',
    expr: 'expr:gte(t\\,n_forced*2)',
    hint: 'Output.ForceKeyFrames: I-frame every 2s.',
  },
  {
    label: 'fade-gate (in then out)',
    expr: 'if(lt(t\\,1)\\,t\\,if(lt(t\\,4)\\,1\\,if(lt(t\\,5)\\,5-t\\,0)))',
    hint: 'volume / alpha: 0→1 over [0,1], hold, 1→0 over [4,5].',
  },
  {
    label: 'conditional (every 5th frame)',
    expr: 'if(eq(mod(n\\,5)\\,0)\\,1\\,0)',
    hint: 'Truthy on n=0,5,10,…',
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

export function ExpressionInput({
  filter,
  option,
  value,
  variables,
  onChange,
  placeholder,
}: Props) {
  const [evalResp, setEvalResp] = useState<EvalResp | null>(null);
  const [evalErr, setEvalErr] = useState<string | null>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const preRef = useRef<HTMLPreElement>(null);
  const id = useId();

  const knownVars = useMemo(() => new Set(variables ?? []), [variables]);

  // Debounced eval. Skip empty.
  useEffect(() => {
    const trimmed = value.trim();
    if (!trimmed) {
      setEvalResp(null);
      setEvalErr(null);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(() => {
      const url = `/api/filters/${encodeURIComponent(filter)}/eval-expression?expr=${encodeURIComponent(trimmed)}`;
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
  }, [filter, value]);

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

  const tokens = highlight(value, knownVars);
  const status = evalErr
    ? { kind: 'err' as const, msg: evalErr }
    : evalResp
      ? evalResp.ok
        ? { kind: 'ok' as const, msg: `= ${formatNum(evalResp.value)} (vars=0)` }
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
          onChange={(e) => onChange(e.target.value)}
          onScroll={handleScroll}
        />
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
          <option value="">Insert pattern…</option>
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
`;
