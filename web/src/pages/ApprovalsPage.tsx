import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { decideApproval, listApprovals, type Decision } from "@/api/approvals";
import { errorMessage } from "@/api/client";
import { keys } from "@/api/keys";
import type { z } from "zod";
import type { approvalSchema } from "@/api/schemas";
import { withoutApprover } from "@/chat/statusText";
import { PageHeader } from "@/components/PageHeader";
import { QueryState } from "@/components/QueryState";
import { Avatar } from "@/components/ui/Avatar";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import "./approvals.css";

type Approval = z.infer<typeof approvalSchema>;

const KIND_TEXT: Record<Approval["kind"], string> = {
  absence_request: "Отсутствие",
  access_request: "Доступ",
  trip_request: "Командировка",
};
/** What happens after the decision, so the lead knows what the button starts (Q&A 10.11: HR steps of a day off). */
const AFTER_TEXT: Record<Approval["kind"], string> = {
  absence_request:
    "После вашего согласования заявку согласует HR-администратор; до этого сотрудник может перенести даты.",
  access_request:
    "Повышенный доступ выдаётся только с вашего согласия; обычные доступы выдаются без вас.",
  trip_request:
    "Билеты и гостиницу после согласования бронирует служба командировок.",
};
const FILTERS: { id: "all" | Approval["kind"]; label: string }[] = [
  { id: "all", label: "Все" },
  { id: "absence_request", label: "Отсутствия" },
  { id: "access_request", label: "Доступы" },
  { id: "trip_request", label: "Командировки" },
];

function ApprovalCard({ approval }: { approval: Approval }) {
  const client = useQueryClient();
  const [comment, setComment] = useState("");
  // one key per card: a double click or a retry replays the same decision instead of creating a second one
  const idempotencyKey = useRef(crypto.randomUUID());
  const decide = useMutation({
    mutationFn: (decision: Decision) =>
      decideApproval(
        approval.id,
        decision,
        comment.trim() || undefined,
        idempotencyKey.current,
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.approvals });
      void client.invalidateQueries({ queryKey: keys.actions });
      void client.invalidateQueries({ queryKey: ["plan"] });
    },
  });
  const busy = decide.isPending || decide.isSuccess;
  // state updates are async: a ref also stops a second click that lands before the buttons are re-rendered as disabled
  const inFlight = useRef(false);
  function submit(decision: Decision) {
    if (inFlight.current) return;
    inFlight.current = true;
    decide.mutate(decision, {
      onError: () => {
        inFlight.current = false;
      },
    });
  }
  return (
    <Card
      className="approval-card"
      aria-label={`Заявка: ${approval.requester_display_name}`}
    >
      <div className="approval-head">
        <Avatar name={approval.requester_display_name} />
        <div className="approval-who">
          <strong>{approval.requester_display_name}</strong>
        </div>
        <span className={`ui-badge ui-badge-kind-${approval.kind}`}>
          {KIND_TEXT[approval.kind]}
        </span>
      </div>
      <p className="approval-summary">{withoutApprover(approval.summary)}</p>
      {approval.team_absent && approval.team_absent.length > 0 && (
        <p className="approval-overlap">В эти дни из команды отсутствуют: {approval.team_absent.join('; ')}</p>
      )}
      {approval.stage === "hr_admin" ? (
        <p className="hint">
          Этап: HR-администратор. Руководитель уже согласовал; после вашего
          согласования отсутствие оформлено, HR BP получит уведомление.
        </p>
      ) : (
        <p className="hint">{AFTER_TEXT[approval.kind]}</p>
      )}
      <label className="approval-comment">
        <span className="hint">Комментарий (необязательно)</span>
        <textarea
          className="ui-input"
          rows={2}
          maxLength={1000}
          value={comment}
          disabled={busy}
          onChange={(e) => setComment(e.target.value)}
          aria-label={`Комментарий для ${approval.requester_display_name}`}
        />
      </label>
      {decide.isError && (
        <p className="ui-field-error">{errorMessage(decide.error)}</p>
      )}
      <div className="approval-actions">
        <Button
          variant="success"
          loading={decide.isPending && decide.variables === "approved"}
          disabled={busy}
          onClick={() => submit("approved")}
        >
          Согласовать
        </Button>
        <Button
          variant="danger"
          loading={decide.isPending && decide.variables === "rejected"}
          disabled={busy}
          onClick={() => submit("rejected")}
        >
          Отклонить
        </Button>
      </div>
    </Card>
  );
}

export function ApprovalsPage() {
  const query = useQuery({
    queryKey: keys.approvals,
    queryFn: listApprovals,
    refetchOnWindowFocus: true,
  });
  const [filter, setFilter] = useState<(typeof FILTERS)[number]["id"]>("all");
  const all = query.data ?? [];
  const shown = filter === "all" ? all : all.filter((a) => a.kind === filter);
  return (
    <section>
      <PageHeader
        title="Согласования"
        hint="Заявки сотрудников, ожидающие вашего решения. Решение сразу видно сотруднику."
        actions={
          all.length > 0 && (
            <div
              className="approval-filter"
              role="group"
              aria-label="Тип заявки"
            >
              {FILTERS.map((f) => {
                const n =
                  f.id === "all"
                    ? all.length
                    : all.filter((a) => a.kind === f.id).length;
                return (
                  <button
                    key={f.id}
                    type="button"
                    aria-pressed={filter === f.id}
                    onClick={() => setFilter(f.id)}
                  >
                    {f.label} · {n}
                  </button>
                );
              })}
            </div>
          )
        }
      />
      <QueryState
        isLoading={query.isPending}
        error={query.error}
        isEmpty={query.data?.length === 0}
        emptyText="Заявок на согласование нет."
        onRetry={() => void query.refetch()}
      >
        {shown.length === 0 && <p className="hint">Заявок этого типа нет.</p>}
        <ul className="approvals-grid">
          {shown.map((a) => (
            <li key={a.id}>
              <ApprovalCard approval={a} />
            </li>
          ))}
        </ul>
      </QueryState>
    </section>
  );
}
