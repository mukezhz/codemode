import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { api, type ChatResult } from "../api";
import { DocumentView } from "../components/Blocks";

export const Route = createFileRoute("/chat")({ component: Ask });

// Questions with no endpoint behind them. Each one needs several capabilities
// composed, filtered, and totalled — which is the work the program does inside
// the sandbox, in one model turn.
const SUGGESTIONS = [
  "give me a status report across every project",
  "what is overdue, and which of it is high priority?",
  "how much did we finish this week compared to what is still open?",
  "chart my completions over the last two weeks",
  "what should I work on next?",
];

type Turn = { request: string; result?: ChatResult; error?: string };

function Ask() {
  const [request, setRequest] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [pending, setPending] = useState(false);

  async function ask(text: string) {
    const question = text.trim();
    if (!question || pending) return;

    setRequest("");
    setPending(true);
    setTurns((previous) => [...previous, { request: question }]);

    try {
      const result = await api.chat(question);
      setTurns((previous) => replaceLast(previous, { request: question, result }));
    } catch (err) {
      setTurns((previous) =>
        replaceLast(previous, { request: question, error: (err as Error).message }),
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="ask">
      {turns.length === 0 && (
        <div className="suggestions">
          <p className="hint">
            One question becomes one program. It calls several capabilities, does the filtering and
            totalling in the sandbox, and returns a document to render — not a sentence.
          </p>
          {SUGGESTIONS.map((suggestion) => (
            <button key={suggestion} className="suggestion" onClick={() => ask(suggestion)}>
              {suggestion}
            </button>
          ))}
        </div>
      )}

      <div className="turns">
        {turns.map((turn, index) => (
          <TurnView key={index} turn={turn} pending={pending && index === turns.length - 1} />
        ))}
      </div>

      <form
        className="composer"
        onSubmit={(event) => {
          event.preventDefault();
          ask(request);
        }}
      >
        <input
          value={request}
          onChange={(event) => setRequest(event.target.value)}
          placeholder="Ask about your work…"
          aria-label="Your question"
          disabled={pending}
        />
        <button type="submit" disabled={pending || !request.trim()}>
          {pending ? "Thinking…" : "Ask"}
        </button>
      </form>
    </div>
  );
}

// ms renders a Go duration, which arrives in nanoseconds.
function ms(nanoseconds: number): string {
  if (nanoseconds < 1_000_000) {
    return `${Math.round(nanoseconds / 1000)}\u00b5s`;
  }
  return `${(nanoseconds / 1_000_000).toFixed(0)}ms`;
}

function TurnView({ turn, pending }: { turn: Turn; pending: boolean }) {
  const [showProgram, setShowProgram] = useState(false);

  return (
    <section className="turn">
      <p className="question">{turn.request}</p>

      {pending && !turn.result && !turn.error && <p className="thinking">Writing a program…</p>}
      {turn.error && <p className="error">{turn.error}</p>}

      {turn.result && (
        <>
          <DocumentView document={turn.result.document} />

          {/* The program is the evidence. When an answer looks wrong, this is
              what tells you whether the model or the data was at fault. */}
          <div className="provenance">
            <button className="link" onClick={() => setShowProgram((open) => !open)}>
              {showProgram ? "Hide" : "Show"} the program
            </button>
            <span className="muted">
              {turn.result.cached
                ? "reused a cached program"
                : `${turn.result.attempts} attempt${turn.result.attempts === 1 ? "" : "s"}`}{" "}
              · {turn.result.took} · {turn.result.used.length} capabilities offered
            </span>
          </div>

          {showProgram && (
            <>
              {/* Where the time actually went. Writing the program is a
                  network round trip to a model; running it is an in-process
                  interpreter, and is normally three orders of magnitude
                  cheaper. A cached answer skips the first entirely. */}
              <p className="muted">
                writing {ms(turn.result.timing.Generate)} · searching{" "}
                {ms(turn.result.timing.Search)} · cache {ms(turn.result.timing.Cache)} · running{" "}
                {ms(turn.result.timing.Run)}
              </p>
              <pre className="program">{turn.result.program}</pre>
              <ul className="offered">
                {turn.result.used.map((match) => (
                  <li key={match.capability.name}>
                    <code>{match.capability.name}</code> {match.capability.description}
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </section>
  );
}

function replaceLast(turns: Turn[], turn: Turn): Turn[] {
  return [...turns.slice(0, -1), turn];
}
