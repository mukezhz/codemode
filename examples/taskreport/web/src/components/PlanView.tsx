import { useState } from "react";
import { api, type Plan } from "../api";

// PlanView shows one plan: what it would change, when it expires, and every
// step it has taken. Approve and Reject send only the ID; the server applies
// exactly what is listed here, once, checking the caller's access again.
export function PlanView({ plan: initial }: { plan: Plan }) {
  const [plan, setPlan] = useState(initial);
  const [error, setError] = useState("");
  const [working, setWorking] = useState(false);

  async function decide(approve: boolean) {
    setWorking(true);
    try {
      const outcome = approve ? await api.approve(plan.id) : await api.reject(plan.id);
      setPlan(outcome.plan);
      setError(outcome.error ?? "");
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setWorking(false);
    }
  }

  return (
    <div className={`plan plan-${plan.status}`}>
      <p className="plan-title">
        <span className={`pill status-${plan.status}`}>{plan.status}</span> {plan.request}
      </p>
      <ul>
        {plan.calls.map((call, index) => (
          <li key={index}>
            <code>{call.name}</code> <span className="muted">{JSON.stringify(call.input)}</span>
            {plan.applied?.[index]?.error && <span className="error"> — {plan.applied[index].error}</span>}
          </li>
        ))}
      </ul>
      {plan.status === "pending" && (
        <div className="plan-actions">
          <button onClick={() => decide(true)} disabled={working}>
            Approve
          </button>
          <button className="secondary" onClick={() => decide(false)} disabled={working}>
            Reject
          </button>
          <span className="muted">expires {when(plan.expires_at)}</span>
        </div>
      )}
      {error && <p className="error">{error}</p>}
      <ol className="timeline">
        {plan.history.map((change, index) => (
          <li key={index}>
            <span className={`dot status-${change.status}`} />
            {change.status} · {when(change.at)}
            {change.by && ` · ${change.by}`}
            {change.note && <span className="muted"> · {change.note}</span>}
          </li>
        ))}
      </ol>
      <p className="muted small">
        plan {plan.id} · run {plan.run_id}
      </p>
    </div>
  );
}

function when(iso: string): string {
  return new Date(iso).toLocaleString();
}
