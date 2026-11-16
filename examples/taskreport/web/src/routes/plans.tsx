import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { api, type Plan } from "../api";
import { PlanView } from "../components/PlanView";

export const Route = createFileRoute("/plans")({ component: Approvals });

// The inbox: plans survive a reload and a restart, so a decision can come later.
function Approvals() {
  const [plans, setPlans] = useState<Plan[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.plans().then((body) => setPlans(body.plans)).catch((err) => setError(err.message));
  }, []);

  if (error) return <p className="error">{error}</p>;
  if (!plans) return <p className="muted">Loading…</p>;
  if (plans.length === 0) {
    return <p className="hint">Nothing has waited for approval yet. Ask the chat to delete a task.</p>;
  }
  const waiting = plans.filter((plan) => plan.status === "pending");
  return (
    <div className="ask">
      <p className="hint">
        {waiting.length} waiting for you · {plans.length - waiting.length} decided
      </p>
      {plans.map((plan) => (
        <PlanView key={plan.id} plan={plan} />
      ))}
    </div>
  );
}
