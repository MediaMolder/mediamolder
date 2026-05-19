// ValidatePanel — displays the result of a validation run.
// Shown in a bottom dock (ValidateDock) similar to RunDock.

import type { Fix, ValidationIssue, ValidationReport, ValidationSeverity } from '../lib/jobTypes';

interface Props {
  report: ValidationReport;
  probeReport: ValidationReport | null;
  isValidating: boolean;
  isProbing: boolean;
  onApplyFix: (fix: Fix, issue: ValidationIssue) => void;
  onRunWithProbe: () => void;
  onClose: () => void;
}

/** Tailored aria-label / colour token per severity. */
const SEV: Record<ValidationSeverity, { label: string; cls: string }> = {
  ERROR:   { label: 'Error',   cls: 'badge-err' },
  WARNING: { label: 'Warning', cls: 'badge-warn' },
  INFO:    { label: 'Info',    cls: 'badge-info' },
};

function SeverityBadge({ severity }: { severity: ValidationSeverity }) {
  const { label, cls } = SEV[severity] ?? SEV.INFO;
  return <span className={`validate-badge ${cls}`}>{label}</span>;
}

/** Strips the "node:" / "output:" / "input:" prefix and returns just the id. */
function stripPrefix(location: string): string {
  return location.replace(/^(node|input|output):/, '');
}

/** Groups and sorts issues: errors first, then warnings, then info. */
function sortIssues(issues: ValidationIssue[]): ValidationIssue[] {
  const order: Record<ValidationSeverity, number> = { ERROR: 0, WARNING: 1, INFO: 2 };
  return [...issues].sort((a, b) => (order[a.severity] ?? 2) - (order[b.severity] ?? 2));
}

export function ValidatePanel({ report, probeReport, isValidating, isProbing, onApplyFix, onRunWithProbe, onClose }: Props) {
  const issues = report.issues ?? [];
  const sorted = sortIssues(issues);
  const errCount  = issues.filter((i) => i.severity === 'ERROR').length;
  const warnCount = issues.filter((i) => i.severity === 'WARNING').length;
  const infoCount = issues.filter((i) => i.severity === 'INFO').length;

  const summary = [
    errCount  ? `${errCount} error${errCount  > 1 ? 's' : ''}` : null,
    warnCount ? `${warnCount} warning${warnCount > 1 ? 's' : ''}` : null,
    infoCount ? `${infoCount} info`                               : null,
  ].filter(Boolean).join(', ') || null;

  const probeIssues = sortIssues(probeReport?.issues ?? []);
  const probeErrCount  = probeIssues.filter((i) => i.severity === 'ERROR').length;
  const probeWarnCount = probeIssues.filter((i) => i.severity === 'WARNING').length;
  const probeInfoCount = probeIssues.filter((i) => i.severity === 'INFO').length;
  const probeSummary = [
    probeErrCount  ? `${probeErrCount} error${probeErrCount  > 1 ? 's' : ''}` : null,
    probeWarnCount ? `${probeWarnCount} warning${probeWarnCount > 1 ? 's' : ''}` : null,
    probeInfoCount ? `${probeInfoCount} info`                                    : null,
  ].filter(Boolean).join(', ') || null;

  return (
    <div className="validate-panel">
      {/* ── header ─────────────────────────────────────── */}
      <div className="validate-panel-header">
        <span className="validate-panel-title">
          Validation Results{isValidating && <span className="validate-spinner" aria-label="Validating…"> ⟳</span>}
        </span>
        <button className="validate-close-btn" onClick={onClose} title="Close validation panel">✕</button>
      </div>

      {/* ── body ───────────────────────────────────────── */}
      <div className="validate-panel-body">
        {/* Static validation results */}
        {sorted.length === 0 ? (
          <div className="validate-empty">
            {isValidating ? 'Checking…' : `✓ No issues detected in this graph.${summary ? ` (${summary})` : ''}`}
          </div>
        ) : (
          <>
            {summary && <div className="validate-section-summary">{summary}</div>}
            <ul className="validate-issue-list">
              {sorted.map((issue, i) => (
                <IssueRow key={i} issue={issue} onApplyFix={onApplyFix} />
              ))}
            </ul>
          </>
        )}

        {/* Probe Inputs button — below static results */}
        <div className="validate-probe-row">
          <button
            className="validate-probe-btn"
            onClick={onRunWithProbe}
            disabled={isValidating || isProbing}
            title="Re-run validation with file probing (Phase B) — opens each input to detect codec/format issues"
          >
            {isProbing ? 'Probing…' : 'Probe Inputs'}
          </button>
        </div>

        {/* Probe results — appended below once probe has run */}
        {probeReport !== null && (
          <div className="validate-probe-results">
            <div className="validate-section-label">
              Probe Results
              {probeSummary && <span className="validate-section-summary">{probeSummary}</span>}
              {isProbing && <span className="validate-spinner" aria-label="Probing…"> ⟳</span>}
            </div>
            {probeIssues.length === 0 ? (
              <div className="validate-empty">✓ No issues found in inputs.</div>
            ) : (
              <ul className="validate-issue-list">
                {probeIssues.map((issue, i) => (
                  <IssueRow key={i} issue={issue} onApplyFix={onApplyFix} />
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function IssueRow({ issue, onApplyFix }: { issue: ValidationIssue; onApplyFix: (fix: Fix, issue: ValidationIssue) => void }) {
  return (
    <li className={`validate-issue validate-issue--${issue.severity.toLowerCase()}`}>
      <div className="validate-issue-header">
        <SeverityBadge severity={issue.severity} />
        <code className="validate-issue-code">{issue.code}</code>
        {issue.location && (
          <span className="validate-issue-location">{stripPrefix(issue.location)}</span>
        )}
      </div>
      <p className="validate-issue-message">{issue.message}</p>
      {issue.suggestion && (
        <p className="validate-issue-suggestion">💡 {issue.suggestion}</p>
      )}
      {issue.fix && (
        <button
          className="validate-fix-btn"
          onClick={() => onApplyFix(issue.fix!, issue)}
          title="Apply this fix to the graph automatically"
        >
          Apply fix
        </button>
      )}
    </li>
  );
}
