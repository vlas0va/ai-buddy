import type { Step } from "../statusText";

/**
 * Цепочка согласования заявки, например руководитель → HR-администратор → HR BP для отгула: три плитки в карточке или
 * полоса прогресса с одной короткой строкой на шаг в узкой панели «Мои заявки».
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
