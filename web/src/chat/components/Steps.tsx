import type { Step } from "../statusText";

/**
 * Approval chain of a request, e.g. lead → HR admin → HR BP for a day off: three tiles in a card, or a progress bar with
 * one short line per step in the narrow «Мои заявки» panel.
 */
export function Steps({
  steps,
  compact = false,
}: {
  steps: Step[];
  compact?: boolean;
}) {
  return (
    <>
      {compact && (
        <div className="chat-steps-bar" aria-hidden="true">
          {steps.map((s) => (
            <span key={s.who} className={`is-${s.state}`} />
          ))}
        </div>
      )}
      <ol
        className={`chat-steps${compact ? " chat-steps-compact" : ""}`}
        aria-label="Шаги согласования"
      >
        {steps.map((s) => (
          <li key={s.who} className={`is-${s.state}`}>
            <b>
              {s.who}
              <span className="steps-colon">:</span>
            </b>{" "}
            {s.text}
          </li>
        ))}
      </ol>
    </>
  );
}
