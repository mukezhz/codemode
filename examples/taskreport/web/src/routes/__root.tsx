import { createRootRoute, Link, Outlet, useRouter } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { api, identity, setIdentity, type Session } from "../api";

export const Route = createRootRoute({ component: Shell });

// The people in the seeded workspace. A real app has a session; this dropdown
// exists so the scoping is visible in one click — switch to grace and the
// admin capabilities appear.
const PEOPLE = [
  { user: "ada", role: "member" },
  { user: "linus", role: "member" },
  { user: "grace", role: "admin" },
];

function Shell() {
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();
  const current = identity();

  useEffect(() => {
    api.session().then(setSession).catch((err) => setError(err.message));
  }, []);

  function switchTo(value: string) {
    const person = PEOPLE.find((entry) => entry.user === value);
    if (!person) return;
    setIdentity(person.user, person.role);
    // Everything on screen is scoped to the caller, so the whole tree is stale.
    router.invalidate();
    window.location.reload();
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="mark" aria-hidden="true" />
          <span>Taskreport</span>
        </div>

        <nav>
          <Link to="/" activeOptions={{ exact: true }}>
            Board
          </Link>
          <Link to="/chat">Ask</Link>
        </nav>

        <div className="who">
          {session && (
            <span className={`pill role-${session.role}`}>
              {session.role}
              {session.withheld > 0 && ` · ${session.withheld} withheld`}
            </span>
          )}
          <select value={current.user} onChange={(event) => switchTo(event.target.value)}>
            {PEOPLE.map((person) => (
              <option key={person.user} value={person.user}>
                {person.user}
              </option>
            ))}
          </select>
        </div>
      </header>

      {error && <p className="error banner">{error}</p>}

      <main>
        <Outlet />
      </main>

      <footer>
        {session?.model ? (
          <span>
            {session.capabilities.length} capabilities across {session.namespaces.join(", ")} ·{" "}
            {session.model}
          </span>
        ) : (
          <span>
            {session?.capabilities.length ?? 0} capabilities · no model configured, so Ask is
            disabled
          </span>
        )}
      </footer>
    </div>
  );
}
