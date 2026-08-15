import { createFileRoute } from "@tanstack/react-router";
import { useCallback, useEffect, useState } from "react";
import { api, type StatusCounts, type Task } from "../api";

export const Route = createFileRoute("/")({ component: Board });

// The board is an ordinary CRUD screen. No model is involved: creating a task
// does not need one, and pretending otherwise would be the tell that this is a
// demo rather than an application.
function Board() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [counts, setCounts] = useState<StatusCounts | null>(null);
  const [status, setStatus] = useState("open");
  const [title, setTitle] = useState("");
  const [project, setProject] = useState("");
  const [priority, setPriority] = useState("medium");
  const [due, setDue] = useState("");
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [list, summary] = await Promise.all([api.tasks({ status }), api.summary()]);
      setTasks(list.tasks ?? []);
      setCounts(summary);
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    }
  }, [status]);

  useEffect(() => {
    load();
  }, [load]);

  async function act(work: Promise<unknown>) {
    try {
      await work;
      await load();
    } catch (err) {
      setError((err as Error).message);
    }
  }

  return (
    <div className="board">
      {counts && (
        <div className="stats">
          <Stat label="Open" value={counts.open} />
          <Stat label="Overdue" value={counts.overdue} tone={counts.overdue > 0 ? "bad" : "none"} />
          <Stat label="Due today" value={counts.due_today} tone="warn" />
          <Stat label="Done" value={counts.completed} tone="good" />
        </div>
      )}

      <form
        className="new-task"
        onSubmit={(event) => {
          event.preventDefault();
          if (!title.trim()) return;
          act(
            api
              .create({ title, project, priority, due_date: due })
              .then(() => {
                setTitle("");
                setDue("");
              }),
          );
        }}
      >
        <input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="What needs doing?"
          aria-label="Task title"
        />
        <input
          value={project}
          onChange={(event) => setProject(event.target.value)}
          placeholder="Project"
          aria-label="Project"
        />
        <select
          value={priority}
          onChange={(event) => setPriority(event.target.value)}
          aria-label="Priority"
        >
          <option value="low">low</option>
          <option value="medium">medium</option>
          <option value="high">high</option>
        </select>
        <input
          type="date"
          value={due}
          onChange={(event) => setDue(event.target.value)}
          aria-label="Due date"
        />
        <button type="submit">Add</button>
      </form>

      <div className="filters">
        {["open", "completed", ""].map((value) => (
          <button
            key={value || "all"}
            className={status === value ? "chip on" : "chip"}
            onClick={() => setStatus(value)}
          >
            {value || "all"}
          </button>
        ))}
      </div>

      {error && <p className="error">{error}</p>}

      <ul className="tasks">
        {tasks.map((task) => (
          <li key={task.id} className={task.completed ? "task done" : "task"}>
            <button
              className="tick"
              aria-label={task.completed ? "Reopen" : "Complete"}
              onClick={() =>
                act(task.completed ? api.reopen(task.id) : api.complete(task.id))
              }
            >
              {task.completed ? "✓" : ""}
            </button>

            <div className="task-body">
              <span className="task-title">{task.title}</span>
              <span className="task-meta">
                {task.project && <span className="tag">{task.project}</span>}
                <span className={`prio prio-${task.priority}`}>{task.priority}</span>
                {task.due_date && <span className="due">due {task.due_date}</span>}
                {task.tags.map((tag) => (
                  <span key={tag} className="tag muted">
                    #{tag}
                  </span>
                ))}
              </span>
            </div>

            <button className="remove" aria-label="Delete" onClick={() => act(api.remove(task.id))}>
              ×
            </button>
          </li>
        ))}
        {tasks.length === 0 && !error && <li className="empty">Nothing here.</li>}
      </ul>
    </div>
  );
}

function Stat({ label, value, tone = "none" }: { label: string; value: number; tone?: string }) {
  return (
    <div className={`stat tone-${tone}`}>
      <span className="stat-value">{value}</span>
      <span className="stat-label">{label}</span>
    </div>
  );
}
